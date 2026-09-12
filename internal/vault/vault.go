// internal/vault/vault.go
//
// Encrypted credential store. Ported from the TetherSSH credential vault
// (MIT, same author) with the UI and session-model coupling removed and the
// record widened for discovery use.
//
// On-disk format: a JSON envelope holding the Argon2id KDF parameters, a
// per-file salt, a per-write nonce, and an AES-256-GCM sealed blob. The
// plaintext is the JSON list of credentials.
//
// Security model (unchanged from the original):
//   - The master password is never written to disk. The 32-byte AES key is
//     derived from it with Argon2id and held in memory only while unlocked.
//   - A wrong master password is detected by GCM authentication failure on
//     unlock; there is no separate password hash to attack.
//   - An app tag is bound as GCM additional-authenticated data, so a tampered
//     envelope header fails to open.
//   - The derived key is zeroized on Lock() and on any failed unlock.
//   - The vault file is private to the user who wrote it: 0600 on Unix, and a
//     protected DACL granting only that user on Windows. See internal/secfile.
//
// This package is pure logic: no UI toolkit, no session model, no network.
// Credential *selection* is deliberately not here; see internal/credres.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/scottpeterman/omegamaps/internal/secfile"
	"golang.org/x/crypto/argon2"
)

// Vault crypto / KDF constants. The KDF parameters are also persisted per-file
// so an older vault keeps opening even if these defaults change later.
const (
	vaultVersion   = 1
	vaultAppTag    = "pathfinder-vault-v1" // GCM additional data (version binding)
	legacyAppTag   = "tetherssh-vault-v1"  // accepted on read for in-place migration
	vaultKDFName   = "argon2id"
	argon2Time     = 3
	argon2Memory   = 64 * 1024 // 64 MiB
	argon2Threads  = 4
	argon2KeyLen   = 32 // AES-256
	vaultSaltLen   = 16
	vaultMinMaster = 8 // minimum master-password length on creation
)

// Vault errors. Callers switch on these.
var (
	// ErrSNMPDefault refuses to make an SNMP credential the default. The
	// default is what a session naming no credential logs in with over SSH,
	// so an SNMP default would put a community string into a password
	// prompt.
	ErrSNMPDefault   = errors.New("an SNMP credential cannot be the default: the default is the SSH login for sessions that name none")
	ErrVaultLocked   = errors.New("credential vault is locked")
	ErrVaultExists   = errors.New("credential vault already exists")
	ErrVaultNotFound = errors.New("credential vault does not exist")
	ErrWrongPassword = errors.New("incorrect master password")
	ErrCredNotFound  = errors.New("credential not found")
	ErrDuplicateName = errors.New("a credential with that name already exists")
	ErrEmptyName     = errors.New("credential name is required")
)

// AuthMethod is the authentication type enum. Kept local to this package so the
// vault has no dependency on the terminal's session model.
type AuthMethod int

const (
	AuthNone AuthMethod = iota
	AuthPassword
	AuthPublicKey
	AuthKeyboardInteractive
	AuthAgent

	// SNMP kinds. Appended so the values above do not move.
	AuthSNMPv2c
	AuthSNMPv3
)

// Canonical stored strings for the SNMP kinds.
const (
	AuthTypeSNMPv2c = "snmp-v2c"
	AuthTypeSNMPv3  = "snmp-v3"
)

func (a AuthMethod) String() string {
	switch a {
	case AuthPassword:
		return "Password"
	case AuthPublicKey:
		return "Public Key"
	case AuthKeyboardInteractive:
		return "Keyboard Interactive"
	case AuthAgent:
		return "Agent"
	case AuthSNMPv2c:
		return "SNMP v2c"
	case AuthSNMPv3:
		return "SNMP v3"
	default:
		return "None"
	}
}

// StringToAuthType maps the canonical stored string to the enum.
func StringToAuthType(s string) AuthMethod {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "publickey", "public_key", "key":
		return AuthPublicKey
	case "keyboard-interactive", "keyboard_interactive", "mfa":
		return AuthKeyboardInteractive
	case "agent":
		return AuthAgent
	case AuthTypeSNMPv2c, "snmpv2c", "snmp_v2c":
		return AuthSNMPv2c
	case AuthTypeSNMPv3, "snmpv3", "snmp_v3":
		return AuthSNMPv3
	default:
		return AuthPassword
	}
}

// Scope narrows which targets a credential is eligible for. A zero Scope is
// unrestricted and matches everything. All populated fields must match.
//
// Specificity (not priority) decides ordering between two eligible
// credentials; see Scope.Specificity.
type Scope struct {
	// DomainSuffix matches a target identity ending in this suffix, e.g.
	// "lab.local". Matching is case-insensitive and label-aligned, so
	// "lab.local" does not match "mylab.local".
	DomainSuffix string `json:"domain_suffix,omitempty"`

	// CIDRs matches a target address inside any listed prefix.
	CIDRs []string `json:"cidrs,omitempty"`

	// Platforms matches a fingerprinted platform string, e.g. "arista_eos".
	// Ignored when the target has not been fingerprinted yet.
	Platforms []string `json:"platforms,omitempty"`
}

// IsZero reports whether the scope is unrestricted.
func (s Scope) IsZero() bool {
	return s.DomainSuffix == "" && len(s.CIDRs) == 0 && len(s.Platforms) == 0
}

// Specificity scores how narrow a scope is. Higher wins. An address match is
// the most specific signal, then platform, then domain suffix weighted by
// label count so "eng.lab.local" outranks "lab.local".
func (s Scope) Specificity() int {
	score := 0
	if len(s.CIDRs) > 0 {
		score += 100
	}
	if len(s.Platforms) > 0 {
		score += 50
	}
	if s.DomainSuffix != "" {
		score += 10 * len(strings.Split(strings.Trim(s.DomainSuffix, "."), "."))
	}
	return score
}

// Credential is one stored set of authentication material. AuthType is the
// canonical string ("password", "publickey", "keyboard-interactive") so it
// stays stable across builds; use Method() to get the AuthMethod enum.
//
// The fields below CreatedAt are additions over the TetherSSH record. They
// carry no secret material and exist so a resolver can order and filter
// candidates without a second store.
//
// SNMP credentials keep their secrets in the SSH fields: v2c stores the
// community in Password; v3 stores the user in Username, the auth key in
// Password and the privacy key in KeyPassphrase. That is deliberate. Every
// build sharing this file format -- Pathfinder, Omega -- rewrites the whole
// vault from its own struct on save and drops fields it does not know, so a
// secret in a new field would be lost the first time one of them saved; in
// these fields it survives, and Redact already clears it. Only the two
// protocol names are new fields, and losing them degrades to the defaults.
type Credential struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Username      string    `json:"username"`
	AuthType      string    `json:"auth_type"`
	Password      string    `json:"password,omitempty"`
	KeyPath       string    `json:"key_path,omitempty"`
	KeyPassphrase string    `json:"key_passphrase,omitempty"`
	IsDefault     bool      `json:"is_default,omitempty"`
	LastUsed      time.Time `json:"last_used,omitempty"`
	CreatedAt     time.Time `json:"created_at"`

	// Description is free text for the picker UI. Never parsed.
	Description string `json:"description,omitempty"`

	// Priority orders candidates within the same scope specificity. Lower
	// runs first. Unset (0) sorts ahead of any explicit positive value, so
	// leaving it alone keeps the credential at the front.
	Priority int `json:"priority,omitempty"`

	// Tags are free-form selectors. A resolver may require that a credential
	// carry every tag the caller asked for.
	Tags []string `json:"tags,omitempty"`

	// Scope restricts eligibility by domain, address, or platform.
	Scope Scope `json:"scope,omitempty"`

	// Disabled takes a credential out of automatic selection without
	// deleting it. It can still be fetched explicitly by ID or name.
	Disabled bool `json:"disabled,omitempty"`

	// SNMPAuthProtocol and SNMPPrivProtocol name an SNMPv3 credential's
	// algorithms: SHA, SHA256, ... and AES, AES256, .... Empty means SHA and
	// AES. Not secret.
	SNMPAuthProtocol string `json:"snmp_auth_protocol,omitempty"`
	SNMPPrivProtocol string `json:"snmp_priv_protocol,omitempty"`
}

// IsSNMP reports whether the credential is for SNMP rather than SSH. The two
// never substitute for each other: a resolver for one must not offer the
// other, or a community string is tried as an SSH password.
func (c Credential) IsSNMP() bool {
	m := c.Method()
	return m == AuthSNMPv2c || m == AuthSNMPv3
}

// Method returns the AuthMethod enum for this credential.
func (c Credential) Method() AuthMethod { return StringToAuthType(c.AuthType) }

// HasTag reports whether the credential carries tag, case-insensitively.
func (c Credential) HasTag(tag string) bool {
	for _, t := range c.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// Redact returns a copy with all secret material cleared. Use it anywhere a
// credential crosses into logging, telemetry, or a UI list.
func (c Credential) Redact() Credential {
	c.Password = ""
	c.KeyPassphrase = ""
	return c
}

// Meta is a redacted view for list/table rendering. It deliberately carries no
// secret material so the manager UI never holds passwords it does not need.
type Meta struct {
	ID          string
	Name        string
	Username    string
	AuthLabel   string
	Description string
	Priority    int
	Tags        []string
	Scope       Scope
	IsDefault   bool
	Disabled    bool
	LastUsed    time.Time
	HasSecret   bool
}

// vaultFile is the on-disk JSON envelope.
type vaultFile struct {
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	KDFTime    uint32 `json:"kdf_time"`
	KDFMemory  uint32 `json:"kdf_memory"`
	KDFThreads uint8  `json:"kdf_threads"`
	Salt       string `json:"salt"`       // base64
	Nonce      string `json:"nonce"`      // base64
	Ciphertext string `json:"ciphertext"` // base64
}

// vaultData is the decrypted plaintext payload.
type vaultData struct {
	Credentials []Credential `json:"credentials"`
}

// Vault manages the encrypted credential store. The zero value is not usable;
// construct via New.
type Vault struct {
	path string

	mu    sync.RWMutex
	key   []byte // 32 bytes when unlocked, nil when locked
	salt  []byte
	creds []Credential

	// Persisted KDF params for the currently-open file (so re-saves match what
	// the file was created with).
	kdfTime    uint32
	kdfMemory  uint32
	kdfThreads uint8
}

// New returns a vault bound to path. The file is not touched until Create or
// Unlock is called.
func New(path string) *Vault {
	return &Vault{
		path:       path,
		kdfTime:    argon2Time,
		kdfMemory:  argon2Memory,
		kdfThreads: argon2Threads,
	}
}

// Path returns the vault file path.
func (v *Vault) Path() string { return v.path }

// Exists reports whether a vault file is present on disk.
func (v *Vault) Exists() bool {
	_, err := os.Stat(v.path)
	return err == nil
}

// IsLocked reports whether the vault is currently locked (no key in memory).
func (v *Vault) IsLocked() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.key == nil
}

// Create initializes a brand-new empty vault protected by master. It fails if a
// vault already exists. On success the vault is left unlocked.
func (v *Vault) Create(master string) error {
	if len(master) < vaultMinMaster {
		return fmt.Errorf("master password must be at least %d characters", vaultMinMaster)
	}
	if v.Exists() {
		return ErrVaultExists
	}

	salt := make([]byte, vaultSaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	v.kdfTime, v.kdfMemory, v.kdfThreads = argon2Time, argon2Memory, argon2Threads
	v.salt = salt
	v.key = deriveKey(master, salt, v.kdfTime, v.kdfMemory, v.kdfThreads)
	v.creds = []Credential{}

	return v.saveLocked()
}

// Unlock derives the key from master and decrypts the vault into memory. A
// wrong password surfaces as ErrWrongPassword (GCM auth failure).
//
// A vault written by TetherSSH opens here as well: the legacy app tag is
// accepted on read, and the next write re-seals under the current tag.
func (v *Vault) Unlock(master string) error {
	raw, err := os.ReadFile(v.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrVaultNotFound
		}
		return fmt.Errorf("failed to read vault: %w", err)
	}

	var f vaultFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("vault file is corrupt: %w", err)
	}
	if f.Version != vaultVersion {
		return fmt.Errorf("unsupported vault version %d", f.Version)
	}

	salt, err := base64.StdEncoding.DecodeString(f.Salt)
	if err != nil {
		return fmt.Errorf("vault salt is corrupt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(f.Nonce)
	if err != nil {
		return fmt.Errorf("vault nonce is corrupt: %w", err)
	}
	ct, err := base64.StdEncoding.DecodeString(f.Ciphertext)
	if err != nil {
		return fmt.Errorf("vault payload is corrupt: %w", err)
	}

	key := deriveKey(master, salt, f.KDFTime, f.KDFMemory, f.KDFThreads)

	plain, err := openGCM(key, nonce, ct, []byte(vaultAppTag))
	if err != nil {
		// Accept a vault sealed by the upstream terminal, then migrate on save.
		plain, err = openGCM(key, nonce, ct, []byte(legacyAppTag))
	}
	if err != nil {
		zero(key)
		return ErrWrongPassword
	}

	var data vaultData
	if err := json.Unmarshal(plain, &data); err != nil {
		zero(key)
		zero(plain)
		return fmt.Errorf("vault contents are corrupt: %w", err)
	}
	zero(plain)

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key != nil {
		zero(v.key)
	}
	v.key = key
	v.salt = salt
	v.kdfTime, v.kdfMemory, v.kdfThreads = f.KDFTime, f.KDFMemory, f.KDFThreads
	v.creds = data.Credentials
	return nil
}

// Lock wipes the in-memory key and credentials. Safe to call when already locked.
func (v *Vault) Lock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key != nil {
		zero(v.key)
		v.key = nil
	}
	for i := range v.creds {
		v.creds[i].Password = ""
		v.creds[i].KeyPassphrase = ""
	}
	v.creds = nil
}

// ChangeMasterPassword re-encrypts the vault under a new master password. It
// verifies the old password against the on-disk file before re-keying.
func (v *Vault) ChangeMasterPassword(oldMaster, newMaster string) error {
	if len(newMaster) < vaultMinMaster {
		return fmt.Errorf("new master password must be at least %d characters", vaultMinMaster)
	}

	raw, err := os.ReadFile(v.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrVaultNotFound
		}
		return fmt.Errorf("failed to read vault: %w", err)
	}
	var f vaultFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("vault file is corrupt: %w", err)
	}
	salt, _ := base64.StdEncoding.DecodeString(f.Salt)
	nonce, _ := base64.StdEncoding.DecodeString(f.Nonce)
	ct, _ := base64.StdEncoding.DecodeString(f.Ciphertext)

	oldKey := deriveKey(oldMaster, salt, f.KDFTime, f.KDFMemory, f.KDFThreads)
	plain, err := openGCM(oldKey, nonce, ct, []byte(vaultAppTag))
	if err != nil {
		plain, err = openGCM(oldKey, nonce, ct, []byte(legacyAppTag))
	}
	zero(oldKey)
	if err != nil {
		return ErrWrongPassword
	}
	zero(plain)

	newSalt := make([]byte, vaultSaltLen)
	if _, err := io.ReadFull(rand.Reader, newSalt); err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key != nil {
		zero(v.key)
	}
	v.kdfTime, v.kdfMemory, v.kdfThreads = argon2Time, argon2Memory, argon2Threads
	v.salt = newSalt
	v.key = deriveKey(newMaster, newSalt, v.kdfTime, v.kdfMemory, v.kdfThreads)
	return v.saveLocked()
}

// All returns a copy of every stored credential, secrets included. This is the
// resolver's entry point; callers must not log the result. Returns
// ErrVaultLocked if the vault is locked.
func (v *Vault) All() ([]Credential, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.key == nil {
		return nil, ErrVaultLocked
	}
	out := make([]Credential, len(v.creds))
	copy(out, v.creds)
	return out, nil
}

// List returns redacted metadata for every credential, sorted by name.
func (v *Vault) List() ([]Meta, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.key == nil {
		return nil, ErrVaultLocked
	}
	out := make([]Meta, 0, len(v.creds))
	for _, c := range v.creds {
		out = append(out, Meta{
			ID:          c.ID,
			Name:        c.Name,
			Username:    c.Username,
			AuthLabel:   c.Method().String(),
			Description: c.Description,
			Priority:    c.Priority,
			Tags:        append([]string(nil), c.Tags...),
			Scope:       c.Scope,
			IsDefault:   c.IsDefault,
			Disabled:    c.Disabled,
			LastUsed:    c.LastUsed,
			HasSecret:   c.Password != "" || c.KeyPassphrase != "" || c.KeyPath != "",
		})
	}
	sortMeta(out)
	return out, nil
}

// Names returns credential names, sorted, for populating selectors.
func (v *Vault) Names() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.key == nil {
		return nil
	}
	names := make([]string, 0, len(v.creds))
	for _, c := range v.creds {
		names = append(names, c.Name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names
}

// Get resolves a credential by exact ID, then exact name, then case-insensitive
// name. Returns a copy. Requires the vault to be unlocked.
func (v *Vault) Get(idOrName string) (Credential, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.key == nil {
		return Credential{}, ErrVaultLocked
	}
	ref := strings.TrimSpace(idOrName)
	for _, c := range v.creds {
		if c.ID == ref {
			return c, nil
		}
	}
	for _, c := range v.creds {
		if c.Name == ref {
			return c, nil
		}
	}
	lower := strings.ToLower(ref)
	for _, c := range v.creds {
		if strings.ToLower(c.Name) == lower {
			return c, nil
		}
	}
	return Credential{}, ErrCredNotFound
}

// Add stores a new credential. ID and CreatedAt are assigned here. Names must be
// unique. If IsDefault is set, any previous default is cleared.
func (v *Vault) Add(c Credential) (Credential, error) {
	if c.IsDefault && c.IsSNMP() {
		return Credential{}, ErrSNMPDefault
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return Credential{}, ErrVaultLocked
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return Credential{}, ErrEmptyName
	}
	for _, existing := range v.creds {
		if strings.EqualFold(existing.Name, c.Name) {
			return Credential{}, ErrDuplicateName
		}
	}
	c.ID = uuid.New().String()
	c.CreatedAt = time.Now()
	if c.IsDefault {
		v.clearDefaultLocked()
	}
	v.creds = append(v.creds, c)
	if err := v.saveLocked(); err != nil {
		return Credential{}, err
	}
	return c, nil
}

// Update replaces an existing credential (matched by ID). CreatedAt is
// preserved. Names must remain unique among other credentials.
func (v *Vault) Update(c Credential) error {
	if c.IsDefault && c.IsSNMP() {
		return ErrSNMPDefault
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return ErrVaultLocked
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return ErrEmptyName
	}
	idx := -1
	for i := range v.creds {
		if v.creds[i].ID == c.ID {
			idx = i
			continue
		}
		if strings.EqualFold(v.creds[i].Name, c.Name) {
			return ErrDuplicateName
		}
	}
	if idx < 0 {
		return ErrCredNotFound
	}
	c.CreatedAt = v.creds[idx].CreatedAt
	if c.IsDefault {
		v.clearDefaultLocked()
	}
	v.creds[idx] = c
	return v.saveLocked()
}

// Delete removes a credential by ID.
func (v *Vault) Delete(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return ErrVaultLocked
	}
	for i := range v.creds {
		if v.creds[i].ID == id {
			v.creds = append(v.creds[:i], v.creds[i+1:]...)
			return v.saveLocked()
		}
	}
	return ErrCredNotFound
}

// SetDefault marks one credential as the default and clears the rest.
func (v *Vault) SetDefault(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return ErrVaultLocked
	}
	// Checked before anything is cleared: the loop below unsets every other
	// default as it goes, and refusing after it would leave that done.
	for _, c := range v.creds {
		if c.ID == id && c.IsSNMP() {
			return ErrSNMPDefault
		}
	}
	found := false
	for i := range v.creds {
		if v.creds[i].ID == id {
			v.creds[i].IsDefault = true
			found = true
		} else {
			v.creds[i].IsDefault = false
		}
	}
	if !found {
		return ErrCredNotFound
	}
	return v.saveLocked()
}

// ClearDefault unsets the default, leaving no credential marked.
//
// A default that can be set and not unset is a trap: "none of them" is a
// legitimate thing to want, and the only way to express it otherwise is to
// promote a credential nobody wants promoted.
func (v *Vault) ClearDefault() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return ErrVaultLocked
	}
	changed := false
	for i := range v.creds {
		if v.creds[i].IsDefault {
			v.creds[i].IsDefault = false
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return v.saveLocked()
}

// Default returns the default credential, if one is set.
//
// A DISABLED credential is not returned. Disabled means "out of automatic
// selection", and being the default is the most automatic there is -- a
// session that names nothing would otherwise authenticate with the one
// credential somebody deliberately took out of service. It stays fetchable by
// Get, which is what disabling is meant to leave working.
func (v *Vault) Default() (Credential, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.key == nil {
		return Credential{}, false
	}
	for _, c := range v.creds {
		if c.IsDefault && !c.Disabled {
			return c, true
		}
	}
	return Credential{}, false
}

// DefaultName is the name of the default credential, or "" when there is none.
//
// It exists so a dialog can SHOW which credential a blank field would use
// without holding secret material: Default() hands back the whole credential,
// and a form that only wants a label has no business carrying a password.
func (v *Vault) DefaultName() string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.key == nil {
		return ""
	}
	for _, c := range v.creds {
		if c.IsDefault && !c.Disabled {
			return c.Name
		}
	}
	return ""
}

// SetDisabled toggles a credential out of (or back into) automatic selection.
func (v *Vault) SetDisabled(id string, disabled bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return ErrVaultLocked
	}
	for i := range v.creds {
		if v.creds[i].ID == id {
			v.creds[i].Disabled = disabled
			return v.saveLocked()
		}
	}
	return ErrCredNotFound
}

// MarkUsed stamps LastUsed=now for the credential and persists it. Best-effort:
// a save failure is logged (without secrets) but not returned, so it never
// blocks a connection.
func (v *Vault) MarkUsed(id string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return
	}
	for i := range v.creds {
		if v.creds[i].ID == id {
			v.creds[i].LastUsed = time.Now()
			if err := v.saveLocked(); err != nil {
				log.Printf("credential vault: could not persist last-used timestamp: %v", err)
			}
			return
		}
	}
}

// clearDefaultLocked unsets IsDefault on all credentials. Caller holds v.mu.
func (v *Vault) clearDefaultLocked() {
	for i := range v.creds {
		v.creds[i].IsDefault = false
	}
}

// saveLocked seals the current credentials with a fresh nonce and writes the
// envelope 0600. Caller holds v.mu and the vault is unlocked.
func (v *Vault) saveLocked() error {
	if v.key == nil {
		return ErrVaultLocked
	}

	plain, err := json.Marshal(vaultData{Credentials: v.creds})
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	nonce, ct, err := sealGCM(v.key, plain, []byte(vaultAppTag))
	zero(plain)
	if err != nil {
		return fmt.Errorf("failed to encrypt vault: %w", err)
	}

	f := vaultFile{
		Version:    vaultVersion,
		KDF:        vaultKDFName,
		KDFTime:    v.kdfTime,
		KDFMemory:  v.kdfMemory,
		KDFThreads: v.kdfThreads,
		Salt:       base64.StdEncoding.EncodeToString(v.salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal vault envelope: %w", err)
	}

	// Atomic replace, private to this user. secfile is what makes the second
	// half true on Windows, where a mode argument is ignored and access comes
	// from the inherited ACL.
	if err := secfile.WriteAtomic(v.path, out); err != nil {
		return fmt.Errorf("failed to write vault: %w", err)
	}
	return nil
}

// deriveKey runs Argon2id to produce the 32-byte AES-256 key.
func deriveKey(master string, salt []byte, t, m uint32, p uint8) []byte {
	return argon2.IDKey([]byte(master), salt, t, m, p, argon2KeyLen)
}

// sealGCM encrypts plaintext with AES-256-GCM, returning a fresh nonce and the
// ciphertext (which includes the auth tag). aad is bound as additional
// authenticated data.
func sealGCM(key, plaintext, aad []byte) (nonce, ciphertext []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, aad)
	return nonce, ciphertext, nil
}

// openGCM decrypts an AES-256-GCM payload. A wrong key or tampered data returns
// an error (authentication failure).
func openGCM(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid nonce size")
	}
	return gcm.Open(nil, nonce, ciphertext, aad)
}

// zero overwrites a byte slice. Best-effort scrub of key/plaintext material.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// sortMeta sorts metadata: default first, then by name (case-insensitive).
func sortMeta(m []Meta) {
	sort.SliceStable(m, func(i, j int) bool {
		if m[i].IsDefault != m[j].IsDefault {
			return m[i].IsDefault
		}
		return strings.ToLower(m[i].Name) < strings.ToLower(m[j].Name)
	})
}

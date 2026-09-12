// app/vault.h
//
// C++ front end for the vault C surface (include/omegamaps/vault.h), ported
// from Omega's qt/omegasshvault.h with SNMP added. Same house rules: QtCore
// types only, no QtWidgets, one owner for every allocation, and nothing that
// hands a secret back.
//
// THE SHAPE IS INHERITED, NOT INVENTED. include/omegamaps/vault.h says it in
// one line -- metadata comes out, secrets only go in -- and this file's job is
// to make that hard to get wrong from C++ rather than merely documented. Hence
// two distinct record types instead of one:
//
//   CredentialMeta   what a read returns. No material fields exist on it, so
//                    there is no member for a UI to accidentally populate and
//                    no member for a redacted round trip to blank out.
//   CredentialInput  what a write takes when the user supplied material.
//
// and two write calls that differ by which type they accept. store() replaces
// a record wholesale, which is why it takes the type that carries material;
// updateMetadata() cannot lose a password because the type it takes has none
// to omit. The sharp corner the C header warns about is a compile error here.
//
// ERRORS. Every call returns VaultError rather than a bool, because the whole
// reason the C surface has codes is that a caller has to branch on why: a
// wrong password re-prompts, an unreachable keyring offers manual credentials
// instead, and neither is served by a message box with one string in it. The
// detail text is captured into lastError() on every non-OK return, so the C
// rule "read it only after a failure" is enforced here once instead of at
// every call site.
//
// THREADING. Safe from any thread; the Go side is mutex-guarded. Three calls
// run Argon2id and cost real time -- create(), unlock() and changeMaster(),
// tens of milliseconds on a many-core box and well over a hundred on one core.
// That is a visible stutter, not a hang, so this wrapper stays synchronous and
// the dialog puts up a busy cursor rather than the application growing a
// worker thread and a signal for something that finishes before a user can
// notice it is gone. If it ever needs to be asynchronous, it needs to be
// asynchronous everywhere, and that is a different change.
//
// LIFETIME. A Vault owns its handle and closes it in the destructor, which
// locks it first. Non-copyable, because two owners of one handle means one of
// them closes it out from under the other. A crawl takes handle(), and the
// Vault must outlive every crawl started with it -- in practice the window
// owns one Vault for the life of the process.

#ifndef OMEGAMAPS_APP_VAULT_H
#define OMEGAMAPS_APP_VAULT_H

#include <QDateTime>
#include <QJsonObject>
#include <QString>
#include <QStringList>
#include <QVector>

namespace omegamaps {

// Mirrors omegamaps_vault_err value for value. Append-only, like the C enum.
enum class VaultError {
    Ok = 0,

    BadHandle = 1,
    BadArgument = 2,

    Locked = 10,
    NotFound = 11,
    Exists = 12,
    WrongPassword = 13,
    Corrupt = 14,
    Io = 15,
    WeakPassword = 16,

    CredNotFound = 20,
    DuplicateName = 21,
    EmptyName = 22,
    SnmpDefault = 23,  // an SNMP credential cannot be the default

    KeyringNoEntry = 30,
    KeyringUnavailable = 31,
    KeyringStale = 32,
    NeedsPassword = 33,

    Internal = 99,
};

// Everything in the keyring range means "the vault could not be reached", as
// opposed to "the vault said no". The distinction drives what a dialog offers,
// so it is a function rather than a comparison spelled out at each site.
bool isKeyringError(VaultError e);

enum class AuthMethod {
    Password,
    PublicKey,
    KeyboardInteractive,
    Agent,
    // SNMP. A v2c community travels in CredentialInput::password; a v3 auth
    // key in password and its privacy key in keyPassphrase, the way the vault
    // stores them.
    SnmpV2c,
    SnmpV3,
};

bool isSnmp(AuthMethod m);

QString authTypeString(AuthMethod m);   // the wire value: "publickey"
QString authLabel(AuthMethod m);        // the display value: "Public Key"
AuthMethod authMethodFromLabel(const QString &label);

// Scope narrows which hosts a credential is offered for. Empty means "any",
// which is what most lab credentials are.
struct CredentialScope {
    QString domainSuffix;
    QStringList cidrs;
    QStringList platforms;

    bool isEmpty() const;

    // A one-line rendering for a table cell: "*.lab.local, 2 CIDRs".
    QString summary() const;

    QJsonObject toJson() const;
    static CredentialScope fromJson(const QJsonObject &o);
};

// The redacted record. Deliberately has no password, key path or passphrase
// member -- see the file comment.
struct CredentialMeta {
    QString id;
    QString name;
    QString username;
    QString authLabel;  // display text from Go
    QString authType;   // the wire value: "password", "snmp-v2c", ...
    bool isSnmp = false;
    QString snmpAuthProtocol;  // v3 only: SHA, SHA256, ...; not secret
    QString snmpPrivProtocol;  // v3 only: AES, AES256, ...; not secret
    QString description;
    int priority = 0;
    QStringList tags;
    CredentialScope scope;
    bool isDefault = false;
    bool disabled = false;
    bool hasSecret = false;
    QDateTime lastUsed;  // invalid when never used

    static CredentialMeta fromJson(const QJsonObject &o);

    // The payload for updateMetadata(). Carries no material and no auth_type,
    // both of which that call refuses.
    QJsonObject toJson() const;
};

// The record a write takes when the user actually supplied material. Every
// field is sent, so an empty one CLEARS what was stored -- that is store()'s
// contract, not an accident, and it is why this type exists separately.
struct CredentialInput {
    QString id;  // empty adds; set replaces
    QString name;
    QString username;
    AuthMethod auth = AuthMethod::Password;
    QString password;
    QString keyPath;
    QString keyPassphrase;
    QString description;
    int priority = 0;
    QStringList tags;
    CredentialScope scope;
    bool isDefault = false;
    bool disabled = false;
    QString snmpAuthProtocol;  // v3 only; empty means SHA
    QString snmpPrivProtocol;  // v3 only; empty means AES

    QJsonObject toJson() const;

    // Overwrites the material fields in place before clearing them. Worth
    // doing and worth being honest about: it reaches the QString buffers this
    // object owns, and it does not reach whatever copies Qt made on the way in
    // from a QLineEdit. Treat it as "this form is done with it", not as a
    // guarantee about the process image -- the same claim omegamaps_vault_lock
    // makes on the Go side.
    void wipeSecrets();
};

// What the quiet-unlock path would find, without the secret.
struct KeyringStatus {
    bool disabled = false;   // the build or the user turned it off
    bool available = false;  // reachable at all
    bool hasEntry = false;   // something is filed for this vault
    QString account;         // the vault path the entry is keyed on
    QString error;           // why it is unavailable; show it, do not parse it
};

class Vault {
public:
    explicit Vault(const QString &path);
    ~Vault();

    Vault(const Vault &) = delete;
    Vault &operator=(const Vault &) = delete;

    // False when the handle could not be opened at all, which is a bad path
    // rather than a missing file. Every call below fails with BadHandle then.
    bool isOpen() const { return handle_ > 0; }

    // For omegamaps_crawl_open. Zero when not open, which is also the value
    // that call takes to mean "no vault", so an unopened vault degrades to a
    // crawl refused for lacking credentials rather than to a bad reference.
    long long handle() const { return handle_ > 0 ? handle_ : 0; }

    QString path() const { return path_; }
    bool exists() const;
    bool isLocked() const;

    // --- unlocking ------------------------------------------------------
    VaultError create(const QString &master);
    VaultError unlock(const QString &master);

    // Keyring, then environment, without prompting. NeedsPassword is the
    // ordinary answer on a machine that has never filed one.
    VaultError unlockQuiet();

    void lock();

    // Does NOT re-file the keyring entry, which is then stale. Follow a
    // success with keyringSet() when an entry exists, or auto-unlock is
    // silently broken until someone notices they are being asked again.
    VaultError changeMaster(const QString &oldMaster, const QString &newMaster);

    // --- credentials ----------------------------------------------------
    VaultError list(QVector<CredentialMeta> *out) const;
    VaultError meta(const QString &idOrName, CredentialMeta *out) const;

    // Adds when input.id is empty, replaces when it is set. Replacing stores
    // exactly what you send.
    VaultError store(const CredentialInput &input, QString *idOut = nullptr);

    // Edits everything except the material. Requires m.id.
    VaultError updateMetadata(const CredentialMeta &m);

    VaultError remove(const QString &id);
    VaultError rename(const QString &id, const QString &newName);
    VaultError setDefault(const QString &id);
    VaultError clearDefault();
    VaultError defaultName(QString *out) const;
    VaultError setDisabled(const QString &id, bool disabled);

    // --- keyring --------------------------------------------------------
    VaultError keyringStatus(KeyringStatus *out) const;

    // The master must already have been verified against the vault. Filing an
    // unverified string is how an entry becomes a lockout, and neither this
    // call nor the one below it can check for you.
    VaultError keyringSet(const QString &master);
    VaultError keyringClear();

    // --- errors ---------------------------------------------------------

    // Detail for the last failure. Empty after a success, because every call
    // clears it -- so a caller cannot show yesterday's message beside today's
    // working call, which is the mistake the C header warns about.
    QString lastError() const { return lastError_; }

    // "OMEGAMAPS_VAULT_ERR_LOCKED". For logs, not for users.
    static QString errorName(VaultError e);

    // One sentence a person can act on, with the detail folded in when there
    // is any. Kept here rather than in a dialog so the unlock path and the
    // manager say the same thing about the same code.
    static QString describe(VaultError e, const QString &detail = QString());

private:
    // Every call funnels its return through here, which captures the detail
    // text on failure and clears it on success. The single place the "only
    // read after a non-OK return" rule lives.
    VaultError finish(int code) const;

    QString path_;
    long long handle_ = -1;
    mutable QString lastError_;
};

}  // namespace omegamaps

#endif  // OMEGAMAPS_APP_VAULT_H

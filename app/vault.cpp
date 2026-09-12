// app/vault.cpp

#include "vault.h"

#include <QJsonArray>
#include <QJsonDocument>

#include <omegamaps/vault.h>

namespace omegamaps {
namespace {

// Every char** the C surface fills belongs to the caller and is released with
// omegamaps_free. Taking it into a QString the moment it arrives means there is
// exactly one release per allocation and no early return can skip it.
QString takeString(char *s) {
    if (!s) return QString();
    const QString out = QString::fromUtf8(s);
    omegamaps_free(s);
    return out;
}

QJsonObject parseObject(const QString &text) {
    return QJsonDocument::fromJson(text.toUtf8()).object();
}

QStringList toStringList(const QJsonArray &a) {
    QStringList out;
    out.reserve(a.size());
    for (const QJsonValue &v : a) out << v.toString();
    return out;
}

QJsonArray fromStringList(const QStringList &list) {
    QJsonArray a;
    for (const QString &s : list) {
        const QString trimmed = s.trimmed();
        if (!trimmed.isEmpty()) a.append(trimmed);
    }
    return a;
}

}  // namespace

bool isSnmp(AuthMethod m) { return m == AuthMethod::SnmpV2c || m == AuthMethod::SnmpV3; }

bool isKeyringError(VaultError e) {
    const int code = static_cast<int>(e);
    return code >= 30 && code < 40;
}

QString authTypeString(AuthMethod m) {
    switch (m) {
        case AuthMethod::PublicKey: return QStringLiteral("publickey");
        case AuthMethod::KeyboardInteractive:
            return QStringLiteral("keyboard-interactive");
        case AuthMethod::Agent: return QStringLiteral("agent");
        case AuthMethod::SnmpV2c: return QStringLiteral("snmp-v2c");
        case AuthMethod::SnmpV3: return QStringLiteral("snmp-v3");
        case AuthMethod::Password: break;
    }
    return QStringLiteral("password");
}

// The labels are Go's, from vault.AuthMethod.String(). They come back on every
// metadata record, so a combo box that produced anything else would show one
// string when editing and another after saving.
QString authLabel(AuthMethod m) {
    switch (m) {
        case AuthMethod::PublicKey: return QStringLiteral("Public Key");
        case AuthMethod::KeyboardInteractive:
            return QStringLiteral("Keyboard Interactive");
        case AuthMethod::Agent: return QStringLiteral("Agent");
        case AuthMethod::SnmpV2c: return QStringLiteral("SNMP v2c");
        case AuthMethod::SnmpV3: return QStringLiteral("SNMP v3");
        case AuthMethod::Password: break;
    }
    return QStringLiteral("Password");
}

AuthMethod authMethodFromLabel(const QString &label) {
    const QString l = label.trimmed().toLower();
    if (l.startsWith(QLatin1String("snmp"))) {
        return l.contains(QLatin1String("v3")) ? AuthMethod::SnmpV3 : AuthMethod::SnmpV2c;
    }
    if (l.startsWith(QLatin1String("public"))) return AuthMethod::PublicKey;
    if (l.startsWith(QLatin1String("keyboard"))) return AuthMethod::KeyboardInteractive;
    if (l.contains(QLatin1String("agent"))) return AuthMethod::Agent;
    return AuthMethod::Password;
}

// ---------------------------------------------------------------------------
// Records

bool CredentialScope::isEmpty() const {
    return domainSuffix.isEmpty() && cidrs.isEmpty() && platforms.isEmpty();
}

QString CredentialScope::summary() const {
    QStringList parts;
    if (!domainSuffix.isEmpty()) parts << QStringLiteral("*.%1").arg(domainSuffix);
    if (!cidrs.isEmpty()) parts << cidrs.join(QStringLiteral(" "));
    if (!platforms.isEmpty()) parts << platforms.join(QStringLiteral(" "));
    return parts.join(QStringLiteral(", "));
}

QJsonObject CredentialScope::toJson() const {
    QJsonObject o;
    o.insert(QStringLiteral("domain_suffix"), domainSuffix.trimmed());
    o.insert(QStringLiteral("cidrs"), fromStringList(cidrs));
    o.insert(QStringLiteral("platforms"), fromStringList(platforms));
    return o;
}

CredentialScope CredentialScope::fromJson(const QJsonObject &o) {
    CredentialScope s;
    s.domainSuffix = o.value(QStringLiteral("domain_suffix")).toString();
    s.cidrs = toStringList(o.value(QStringLiteral("cidrs")).toArray());
    s.platforms = toStringList(o.value(QStringLiteral("platforms")).toArray());
    return s;
}

CredentialMeta CredentialMeta::fromJson(const QJsonObject &o) {
    CredentialMeta m;
    m.id = o.value(QStringLiteral("id")).toString();
    m.name = o.value(QStringLiteral("name")).toString();
    m.username = o.value(QStringLiteral("username")).toString();
    m.authLabel = o.value(QStringLiteral("auth_label")).toString();
    m.authType = o.value(QStringLiteral("auth_type")).toString();
    m.isSnmp = o.value(QStringLiteral("is_snmp")).toBool();
    m.snmpAuthProtocol = o.value(QStringLiteral("snmp_auth_protocol")).toString();
    m.snmpPrivProtocol = o.value(QStringLiteral("snmp_priv_protocol")).toString();
    m.description = o.value(QStringLiteral("description")).toString();
    m.priority = o.value(QStringLiteral("priority")).toInt();
    m.tags = toStringList(o.value(QStringLiteral("tags")).toArray());
    m.scope = CredentialScope::fromJson(o.value(QStringLiteral("scope")).toObject());
    m.isDefault = o.value(QStringLiteral("is_default")).toBool();
    m.disabled = o.value(QStringLiteral("disabled")).toBool();
    m.hasSecret = o.value(QStringLiteral("has_secret")).toBool();

    // Absent when never used, which is not the same as the epoch -- an invalid
    // QDateTime is what lets a table print "never" rather than 1970.
    const QString used = o.value(QStringLiteral("last_used")).toString();
    if (!used.isEmpty()) m.lastUsed = QDateTime::fromString(used, Qt::ISODate);
    return m;
}

QJsonObject CredentialMeta::toJson() const {
    QJsonObject o;
    o.insert(QStringLiteral("id"), id);
    o.insert(QStringLiteral("name"), name.trimmed());
    o.insert(QStringLiteral("username"), username.trimmed());
    o.insert(QStringLiteral("description"), description);
    o.insert(QStringLiteral("priority"), priority);
    o.insert(QStringLiteral("tags"), fromStringList(tags));
    o.insert(QStringLiteral("scope"), scope.toJson());
    o.insert(QStringLiteral("is_default"), isDefault);
    o.insert(QStringLiteral("disabled"), disabled);
    // No auth_type and no material: update_meta refuses both, and this type
    // has nowhere to hold them anyway.
    return o;
}

QJsonObject CredentialInput::toJson() const {
    QJsonObject o;
    if (!id.isEmpty()) o.insert(QStringLiteral("id"), id);
    o.insert(QStringLiteral("name"), name.trimmed());
    o.insert(QStringLiteral("username"), username.trimmed());
    o.insert(QStringLiteral("auth_type"), authTypeString(auth));
    if (!snmpAuthProtocol.isEmpty())
        o.insert(QStringLiteral("snmp_auth_protocol"), snmpAuthProtocol.trimmed());
    if (!snmpPrivProtocol.isEmpty())
        o.insert(QStringLiteral("snmp_priv_protocol"), snmpPrivProtocol.trimmed());
    if (!password.isEmpty()) o.insert(QStringLiteral("password"), password);
    if (!keyPath.isEmpty()) o.insert(QStringLiteral("key_path"), keyPath.trimmed());
    if (!keyPassphrase.isEmpty())
        o.insert(QStringLiteral("key_passphrase"), keyPassphrase);
    o.insert(QStringLiteral("description"), description);
    o.insert(QStringLiteral("priority"), priority);
    o.insert(QStringLiteral("tags"), fromStringList(tags));
    o.insert(QStringLiteral("scope"), scope.toJson());
    o.insert(QStringLiteral("is_default"), isDefault);
    o.insert(QStringLiteral("disabled"), disabled);
    return o;
}

void CredentialInput::wipeSecrets() {
    for (QString *s : {&password, &keyPassphrase}) {
        s->fill(QLatin1Char('\0'));
        s->clear();
    }
}

// ---------------------------------------------------------------------------
// Vault

Vault::Vault(const QString &path) : path_(path) {
    handle_ = omegamaps_vault_open(path.toUtf8().constData());
    if (handle_ <= 0) {
        lastError_ = takeString(omegamaps_last_error());
    }
}

Vault::~Vault() {
    if (handle_ > 0) omegamaps_vault_close(handle_);
}

VaultError Vault::finish(int code) const {
    const auto e = static_cast<VaultError>(code);
    // Cleared on success rather than left readable. The two surfaces share one
    // error store, so a stale string beside a call that worked is somebody
    // else's failure being reported as this one's.
    lastError_ = (e == VaultError::Ok) ? QString()
                                       : takeString(omegamaps_last_error());
    return e;
}

bool Vault::exists() const {
    return handle_ > 0 && omegamaps_vault_exists(handle_) == 1;
}

bool Vault::isLocked() const {
    // A bad handle reports locked, which is the safe answer and the one the C
    // surface already gives.
    return handle_ <= 0 || omegamaps_vault_is_locked(handle_) == 1;
}

VaultError Vault::create(const QString &master) {
    return finish(omegamaps_vault_create(handle_, master.toUtf8().constData()));
}

VaultError Vault::unlock(const QString &master) {
    return finish(omegamaps_vault_unlock(handle_, master.toUtf8().constData()));
}

VaultError Vault::unlockQuiet() {
    return finish(omegamaps_vault_unlock_quiet(handle_));
}

void Vault::lock() {
    if (handle_ > 0) omegamaps_vault_lock(handle_);
}

VaultError Vault::changeMaster(const QString &oldMaster, const QString &newMaster) {
    return finish(omegamaps_vault_change_master(handle_,
                                               oldMaster.toUtf8().constData(),
                                               newMaster.toUtf8().constData()));
}

VaultError Vault::list(QVector<CredentialMeta> *out) const {
    char *json = nullptr;
    const VaultError e = finish(omegamaps_vault_list(handle_, &json));
    const QString text = takeString(json);
    if (e != VaultError::Ok || !out) return e;

    out->clear();
    const QJsonArray records = QJsonDocument::fromJson(text.toUtf8()).array();
    out->reserve(records.size());
    for (const QJsonValue &v : records) {
        out->append(CredentialMeta::fromJson(v.toObject()));
    }
    return e;
}

VaultError Vault::meta(const QString &idOrName, CredentialMeta *out) const {
    char *json = nullptr;
    const VaultError e =
        finish(omegamaps_vault_meta(handle_, idOrName.toUtf8().constData(), &json));
    const QString text = takeString(json);
    if (e == VaultError::Ok && out) *out = CredentialMeta::fromJson(parseObject(text));
    return e;
}

VaultError Vault::store(const CredentialInput &input, QString *idOut) {
    const QByteArray payload = QJsonDocument(input.toJson()).toJson(QJsonDocument::Compact);
    char *id = nullptr;
    const VaultError e = finish(omegamaps_vault_store(handle_, payload.constData(), &id));
    const QString newId = takeString(id);
    if (e == VaultError::Ok && idOut) *idOut = newId;
    return e;
}

VaultError Vault::updateMetadata(const CredentialMeta &m) {
    const QByteArray payload = QJsonDocument(m.toJson()).toJson(QJsonDocument::Compact);
    return finish(omegamaps_vault_update_meta(handle_, payload.constData()));
}

VaultError Vault::remove(const QString &id) {
    return finish(omegamaps_vault_delete(handle_, id.toUtf8().constData()));
}

VaultError Vault::rename(const QString &id, const QString &newName) {
    return finish(omegamaps_vault_rename(handle_, id.toUtf8().constData(),
                                        newName.trimmed().toUtf8().constData()));
}

VaultError Vault::setDefault(const QString &id) {
    return finish(omegamaps_vault_set_default(handle_, id.toUtf8().constData()));
}

VaultError Vault::clearDefault() {
    return finish(omegamaps_vault_clear_default(handle_));
}

VaultError Vault::defaultName(QString *out) const {
    char *name = nullptr;
    const VaultError e = finish(omegamaps_vault_default_name(handle_, &name));
    const QString text = takeString(name);
    if (e == VaultError::Ok && out) *out = text;
    return e;
}

VaultError Vault::setDisabled(const QString &id, bool disabled) {
    return finish(
        omegamaps_vault_set_disabled(handle_, id.toUtf8().constData(), disabled ? 1 : 0));
}

VaultError Vault::keyringStatus(KeyringStatus *out) const {
    char *json = nullptr;
    const VaultError e = finish(omegamaps_vault_keyring_status(handle_, &json));
    const QString text = takeString(json);
    if (e != VaultError::Ok || !out) return e;

    const QJsonObject o = parseObject(text);
    out->disabled = o.value(QStringLiteral("disabled")).toBool();
    out->available = o.value(QStringLiteral("available")).toBool();
    out->hasEntry = o.value(QStringLiteral("has_entry")).toBool();
    out->account = o.value(QStringLiteral("account")).toString();
    out->error = o.value(QStringLiteral("error")).toString();
    return e;
}

VaultError Vault::keyringSet(const QString &master) {
    return finish(omegamaps_vault_keyring_set(handle_, master.toUtf8().constData()));
}

VaultError Vault::keyringClear() {
    return finish(omegamaps_vault_keyring_clear(handle_));
}

QString Vault::errorName(VaultError e) {
    // Owned by the library and not freed -- the one exception to the
    // omegamaps_free rule, because it is a constant.
    return QString::fromUtf8(
        omegamaps_vault_error_name(static_cast<omegamaps_vault_err>(e)));
}

QString Vault::describe(VaultError e, const QString &detail) {
    QString text;
    switch (e) {
        case VaultError::Ok:
            return QString();
        case VaultError::BadHandle:
            text = QStringLiteral("The vault is not open.");
            break;
        case VaultError::BadArgument:
            text = QStringLiteral("The vault refused the request as malformed.");
            break;
        case VaultError::Locked:
            text = QStringLiteral("The vault is locked.");
            break;
        case VaultError::NotFound:
            text = QStringLiteral("No vault file at that path.");
            break;
        case VaultError::Exists:
            text = QStringLiteral("A vault already exists at that path.");
            break;
        case VaultError::WrongPassword:
            text = QStringLiteral("That master password did not open the vault.");
            break;
        case VaultError::Corrupt:
            text = QStringLiteral("The vault file could not be read as a vault.");
            break;
        case VaultError::Io:
            text = QStringLiteral("The vault file could not be read or written.");
            break;
        case VaultError::WeakPassword:
            text = QStringLiteral("That master password is too short.");
            break;
        case VaultError::CredNotFound:
            text = QStringLiteral("No credential by that name.");
            break;
        case VaultError::DuplicateName:
            text = QStringLiteral("A credential with that name already exists.");
            break;
        case VaultError::EmptyName:
            text = QStringLiteral("A credential needs a name.");
            break;
        case VaultError::SnmpDefault:
            text = QStringLiteral(
                "An SNMP credential cannot be the default: the default is the SSH login "
                "for anything that names none.");
            break;
        case VaultError::KeyringNoEntry:
            text = QStringLiteral("Nothing is filed in the OS keyring for this vault.");
            break;
        case VaultError::KeyringUnavailable:
            text = QStringLiteral("The OS keyring could not be reached.");
            break;
        case VaultError::KeyringStale:
            // Never "wrong password". The user typed nothing, and telling them
            // they mistyped sends them looking in the wrong place.
            text = QStringLiteral(
                "The password saved in the OS keyring no longer opens this vault.");
            break;
        case VaultError::NeedsPassword:
            text = QStringLiteral("The vault needs its master password.");
            break;
        case VaultError::Internal:
            text = QStringLiteral("The vault reported an internal error.");
            break;
    }
    if (detail.isEmpty()) return text;
    return QStringLiteral("%1\n\n%2").arg(text, detail);
}

}  // namespace omegamaps

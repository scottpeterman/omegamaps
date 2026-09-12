// app/vaultdialogs.cpp

#include "vaultdialogs.h"

#include <QApplication>
#include <QCheckBox>
#include <QComboBox>
#include <QDialogButtonBox>
#include <QFileInfo>
#include <QFormLayout>
#include <QLabel>
#include <QLineEdit>
#include <QPushButton>
#include <QVBoxLayout>

#include "crawlform.h"
#include "theme.h"
#include "vault.h"

namespace omegamaps {

namespace {

QLabel *errorLabel(QWidget *parent) {
    auto *l = new QLabel(parent);
    l->setProperty("tone", QStringLiteral("danger"));
    l->setWordWrap(true);
    l->hide();
    return l;
}

void showError(QLabel *l, const QString &text) {
    l->setText(text);
    l->setVisible(!text.isEmpty());
}

// Argon2id takes long enough to notice; say so rather than look frozen.
struct BusyCursor {
    BusyCursor() { QApplication::setOverrideCursor(Qt::WaitCursor); }
    ~BusyCursor() { QApplication::restoreOverrideCursor(); }
};

}  // namespace

MasterPasswordDialog::MasterPasswordDialog(Vault *vault, QWidget *parent)
    : QDialog(parent), m_vault(vault), m_create(!vault->exists()) {
    setWindowTitle(m_create ? tr("Create credential vault") : tr("Unlock credential vault"));
    auto *col = new QVBoxLayout(this);

    auto *intro = new QLabel(m_create
        ? tr("No vault at %1 yet. Choose a master password for it: at least 8 characters. "
             "It encrypts every credential and cannot be recovered.").arg(vault->path())
        : tr("Master password for %1").arg(vault->path()), this);
    intro->setWordWrap(true);
    intro->setProperty("tone", QStringLiteral("secondary"));
    col->addWidget(intro);

    auto *form = new QFormLayout;
    m_password = new QLineEdit(this);
    m_password->setEchoMode(QLineEdit::Password);
    form->addRow(tr("Master password"), m_password);
    if (m_create) {
        m_confirm = new QLineEdit(this);
        m_confirm->setEchoMode(QLineEdit::Password);
        form->addRow(tr("Again"), m_confirm);
    }
    col->addLayout(form);

    m_remember = new QCheckBox(tr("Remember in the OS keyring"), this);
    m_remember->setToolTip(tr("Unlock without asking next time. Stored by the OS, keyed on this vault's path."));
    col->addWidget(m_remember);

    m_error = errorLabel(this);
    col->addWidget(m_error);

    auto *buttons = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel, this);
    buttons->button(QDialogButtonBox::Ok)->setText(m_create ? tr("Create") : tr("Unlock"));
    buttons->button(QDialogButtonBox::Ok)->setProperty("primary", QStringLiteral("true"));
    connect(buttons, &QDialogButtonBox::accepted, this, &MasterPasswordDialog::attempt);
    connect(buttons, &QDialogButtonBox::rejected, this, &QDialog::reject);
    col->addWidget(buttons);
    setMinimumWidth(420);
}

void MasterPasswordDialog::attempt() {
    const QString pw = m_password->text();
    if (m_create && pw != m_confirm->text()) {
        showError(m_error, tr("The two passwords differ."));
        return;
    }
    VaultError e;
    {
        BusyCursor busy;
        e = m_create ? m_vault->create(pw) : m_vault->unlock(pw);
    }
    if (e != VaultError::Ok) {
        showError(m_error, Vault::describe(e, m_vault->lastError()));
        m_password->selectAll();
        m_password->setFocus();
        return;
    }
    if (m_remember->isChecked()) {
        // The vault just accepted it, which is the only thing that makes
        // filing it safe. A keyring that will not take it is not a reason to
        // fail an unlock that worked.
        m_vault->keyringSet(pw);
    }
    m_password->clear();
    if (m_confirm) m_confirm->clear();
    accept();
}

CredentialDialog::CredentialDialog(Vault *vault, QWidget *parent) : QDialog(parent), m_vault(vault) {
    setWindowTitle(tr("Add credential"));
    auto *col = new QVBoxLayout(this);
    auto *form = new QFormLayout;

    m_name = new QLineEdit(this);
    m_name->setPlaceholderText(tr("lab"));
    form->addRow(tr("Name"), m_name);

    m_type = new QComboBox(this);
    const std::pair<AuthMethod, const char *> types[] = {
        {AuthMethod::Password, "SSH password"},
        {AuthMethod::PublicKey, "SSH key"},
        {AuthMethod::Agent, "SSH agent"},
        {AuthMethod::SnmpV2c, "SNMP v2c"},
        {AuthMethod::SnmpV3, "SNMP v3"},
    };
    for (const auto &[m, label] : types) m_type->addItem(tr(label), static_cast<int>(m));
    form->addRow(tr("Type"), m_type);

    m_userLabel = new QLabel(tr("Username"), this);
    m_user = new QLineEdit(this);
    form->addRow(m_userLabel, m_user);

    m_secretLabel = new QLabel(tr("Password"), this);
    m_secret = new QLineEdit(this);
    m_secret->setEchoMode(QLineEdit::Password);
    form->addRow(m_secretLabel, m_secret);

    m_keyLabel = new QLabel(tr("Key file"), this);
    m_keyPath = new QLineEdit(this);
    m_keyPath->setPlaceholderText(QStringLiteral("~/.ssh/id_ed25519"));
    form->addRow(m_keyLabel, m_keyPath);

    m_privLabel = new QLabel(tr("Privacy key"), this);
    m_priv = new QLineEdit(this);
    m_priv->setEchoMode(QLineEdit::Password);
    form->addRow(m_privLabel, m_priv);

    m_tags = new QLineEdit(this);
    m_tags->setPlaceholderText(tr("lab, core"));
    m_tags->setToolTip(tr("A crawl can be limited to credentials carrying given tags"));
    form->addRow(tr("Tags"), m_tags);
    col->addLayout(form);

    m_error = errorLabel(this);
    col->addWidget(m_error);

    auto *buttons = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel, this);
    buttons->button(QDialogButtonBox::Ok)->setText(tr("Add"));
    buttons->button(QDialogButtonBox::Ok)->setProperty("primary", QStringLiteral("true"));
    connect(buttons, &QDialogButtonBox::accepted, this, &CredentialDialog::attempt);
    connect(buttons, &QDialogButtonBox::rejected, this, &QDialog::reject);
    col->addWidget(buttons);

    connect(m_type, qOverload<int>(&QComboBox::currentIndexChanged), this, &CredentialDialog::typeChanged);
    typeChanged();
    setMinimumWidth(440);
}

// Which fields a type has, and what they are called. The storage follows the
// vault: a community and a v3 auth key both live in the password field.
void CredentialDialog::typeChanged() {
    const auto m = static_cast<AuthMethod>(m_type->currentData().toInt());
    const bool v2c = m == AuthMethod::SnmpV2c, v3 = m == AuthMethod::SnmpV3;
    m_userLabel->setText(v3 ? tr("v3 user") : tr("Username"));
    m_userLabel->setVisible(!v2c);
    m_user->setVisible(!v2c);
    m_secretLabel->setText(v2c ? tr("Community") : v3 ? tr("Auth key")
                           : m == AuthMethod::PublicKey ? tr("Passphrase") : tr("Password"));
    const bool hasSecret = m != AuthMethod::Agent;
    m_secretLabel->setVisible(hasSecret);
    m_secret->setVisible(hasSecret);
    m_keyLabel->setVisible(m == AuthMethod::PublicKey);
    m_keyPath->setVisible(m == AuthMethod::PublicKey);
    m_privLabel->setVisible(v3);
    m_priv->setVisible(v3);
    adjustSize();
}

void CredentialDialog::attempt() {
    const auto m = static_cast<AuthMethod>(m_type->currentData().toInt());
    CredentialInput in;
    in.name = m_name->text().trimmed();
    in.auth = m;
    in.username = m == AuthMethod::SnmpV2c ? QString() : m_user->text().trimmed();
    in.tags = splitList(m_tags->text());
    if (m == AuthMethod::PublicKey) {
        in.keyPath = m_keyPath->text().trimmed();
        in.keyPassphrase = m_secret->text();
    } else if (m == AuthMethod::SnmpV3) {
        in.password = m_secret->text();
        in.keyPassphrase = m_priv->text();
    } else if (m != AuthMethod::Agent) {
        in.password = m_secret->text();
    }

    // What the vault would accept but a crawl could not use.
    QString missing;
    if (in.name.isEmpty()) missing = tr("A name is required.");
    else if (m != AuthMethod::SnmpV2c && in.username.isEmpty()) missing = tr("A username is required.");
    else if (m == AuthMethod::SnmpV2c && in.password.isEmpty()) missing = tr("A community is required.");
    else if (m == AuthMethod::PublicKey && in.keyPath.isEmpty()) missing = tr("A key file is required.");
    else if ((m == AuthMethod::Password || m == AuthMethod::SnmpV3) && in.password.isEmpty())
        missing = m == AuthMethod::SnmpV3 ? tr("An auth key is required.") : tr("A password is required.");
    if (!missing.isEmpty()) {
        showError(m_error, missing);
        return;
    }

    const VaultError e = m_vault->store(in, &m_id);
    in.wipeSecrets();
    if (e != VaultError::Ok) {
        showError(m_error, Vault::describe(e, m_vault->lastError()));
        return;
    }
    m_secret->clear();
    m_priv->clear();
    accept();
}

}  // namespace omegamaps

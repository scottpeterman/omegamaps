// app/vaultdialogs.h
//
// The two dialogs the credentials panel needs: the master password (unlock,
// or create a vault that does not exist yet), and a new credential.
//
// Both do their vault call themselves and stay open on failure with the
// reason inline, so "wrong password" is one retype rather than a message box
// and a reopen. Neither holds a secret after it closes: the password fields
// are cleared on the way out.

#ifndef OMEGAMAPS_APP_VAULTDIALOGS_H
#define OMEGAMAPS_APP_VAULTDIALOGS_H

#include <QDialog>

class QCheckBox;
class QComboBox;
class QLabel;
class QLineEdit;

namespace omegamaps {

class Vault;

class MasterPasswordDialog : public QDialog {
    Q_OBJECT
public:
    // Creates the vault when it does not exist yet, unlocks it otherwise.
    MasterPasswordDialog(Vault *vault, QWidget *parent = nullptr);

private:
    void attempt();

    Vault *m_vault;
    bool m_create;
    QLineEdit *m_password = nullptr;
    QLineEdit *m_confirm = nullptr;
    QCheckBox *m_remember = nullptr;
    QLabel *m_error = nullptr;
};

class CredentialDialog : public QDialog {
    Q_OBJECT
public:
    CredentialDialog(Vault *vault, QWidget *parent = nullptr);
    QString storedId() const { return m_id; }

private:
    void typeChanged();
    void attempt();

    Vault *m_vault;
    QString m_id;
    QLineEdit *m_name = nullptr;
    QComboBox *m_type = nullptr;
    QLabel *m_userLabel = nullptr;
    QLineEdit *m_user = nullptr;
    QLabel *m_secretLabel = nullptr;
    QLineEdit *m_secret = nullptr;
    QLabel *m_keyLabel = nullptr;
    QLineEdit *m_keyPath = nullptr;
    QLabel *m_privLabel = nullptr;
    QLineEdit *m_priv = nullptr;
    QLineEdit *m_tags = nullptr;
    QLabel *m_error = nullptr;
};

}  // namespace omegamaps

#endif

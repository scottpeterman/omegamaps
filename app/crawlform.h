// app/crawlform.h
//
// The crawl form: Secure Cartography's left and middle columns -- connection,
// credentials, discovery options, output, and the run buttons -- over the C
// surface's crawl request.
//
// The form holds no crawl logic. It turns its fields into the JSON
// omegamaps_crawl_open takes, asks omegamaps_crawl_validate what is wrong
// with it, and marks those fields; defaults come from omegamaps_crawl_defaults,
// so the window cannot disagree with the command line about what an untouched
// field means. Credentials never pass through it: the credentials panel lists
// what the vault holds, and a crawl is handed the vault itself.
//
// Where sc2 used chip-style tag inputs, the list fields here are plain text --
// seeds split on newlines, commas, semicolons or spaces, the others on commas
// or spaces -- because a paste out of a spreadsheet or a ticket is the common
// case and a chip widget fights it.

#ifndef OMEGAMAPS_APP_CRAWLFORM_H
#define OMEGAMAPS_APP_CRAWLFORM_H

#include <QHash>
#include <QObject>
#include <QVector>

#include "panel.h"

class QCheckBox;
class QComboBox;
class QLabel;
class QLineEdit;
class QPlainTextEdit;
class QPushButton;
class QSpinBox;
class QTableWidget;

namespace omegamaps {

class Vault;

class ConnectionPanel : public Panel {
    Q_OBJECT
public:
    explicit ConnectionPanel(QWidget *parent = nullptr);
    QPlainTextEdit *seeds = nullptr;
    QLineEdit *domains = nullptr;
    QLineEdit *allowDomains = nullptr;
    QLineEdit *exclude = nullptr;
};

class CredentialsPanel : public Panel {
    Q_OBJECT
public:
    CredentialsPanel(Vault *vault, QWidget *parent = nullptr);

    // Re-reads the vault: its state, and the table when it is unlocked.
    void refresh();
    void setVault(Vault *vault);

    QLineEdit *credTags = nullptr;
    QLabel *status = nullptr;
    QTableWidget *table = nullptr;
    QPushButton *unlockBtn = nullptr;
    QPushButton *addBtn = nullptr;
    QPushButton *removeBtn = nullptr;

signals:
    void vaultChanged();

private:
    void unlockOrLock();
    void addCredential();
    void removeSelected();

    Vault *m_vault;
};

class OptionsPanel : public Panel {
    Q_OBJECT
public:
    explicit OptionsPanel(QWidget *parent = nullptr);
    QSpinBox *depth = nullptr;
    QSpinBox *concurrency = nullptr;
    QSpinBox *timeoutS = nullptr;
    QSpinBox *snmpTimeoutS = nullptr;
    QComboBox *methods = nullptr;
    QComboBox *hostKeys = nullptr;
    QLineEdit *knownHosts = nullptr;
    QCheckBox *legacy = nullptr;
    QCheckBox *trustUni = nullptr;
};

class OutputPanel : public Panel {
    Q_OBJECT
public:
    explicit OutputPanel(QWidget *parent = nullptr);
    QString mapPath() const;  // empty when either field is
    QLineEdit *directory = nullptr;
    QLineEdit *mapName = nullptr;
    QLabel *resolved = nullptr;

private:
    void updateResolved();
};

class RunPanel : public Panel {
    Q_OBJECT
public:
    explicit RunPanel(QWidget *parent = nullptr);
    QPushButton *start = nullptr;
    QPushButton *testSingle = nullptr;
    QPushButton *stop = nullptr;
    QPushButton *viewMap = nullptr;  // the last run's map, in the map viewer
    QLabel *status = nullptr;    // what the run is doing, or how it ended
    QLabel *problems = nullptr;  // why the last start was refused

    // Sets the status line and its colour: accent, success, warning, danger,
    // muted.
    void setStatus(const QString &text, const QString &tone);
};

// Owns nothing on screen; ties the panels to a request and to the settings.
class CrawlForm : public QObject {
    Q_OBJECT
public:
    CrawlForm(Vault *vault, ConnectionPanel *conn, CredentialsPanel *creds, OptionsPanel *opts,
              OutputPanel *out, RunPanel *run, QObject *parent = nullptr);

    // The request the fields describe. testSingle is sc2's Test Single: the
    // first seed alone, depth 0, written beside the map as <name>-test.json.
    QByteArray request(bool testSingle = false) const;

    // Asks the C surface what is wrong with the request and marks those
    // fields. True when nothing is.
    bool validate(const QByteArray &request);

    void load();  // from QSettings, over the library's defaults
    void save() const;

    void setRunning(bool running);
    void setVault(Vault *vault) { m_vault = vault; }

    // Field names as the request names them, mapped to the widgets that set
    // them. Exposed so the probe can check which widget got marked.
    QWidget *fieldWidget(const QString &field) const { return m_fields.value(field); }

signals:
    void startRequested(const QByteArray &request);
    void stopRequested();

private:
    void startClicked(bool testSingle);
    void applyDefaults();
    void clearMarks();

    Vault *m_vault;
    ConnectionPanel *m_conn;
    CredentialsPanel *m_creds;
    OptionsPanel *m_opts;
    OutputPanel *m_out;
    RunPanel *m_run;
    QHash<QString, QWidget *> m_fields;
};

// Comma/space/semicolon/newline-separated text as a list, empties dropped.
QStringList splitList(const QString &text);

}  // namespace omegamaps

#endif

// app/crawlform.cpp

#include "crawlform.h"
#include "filedialogs.h"

#include <QCheckBox>
#include <QComboBox>
#include <QDir>
#include <QFileInfo>
#include <QGridLayout>
#include <QHBoxLayout>
#include <QHeaderView>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QLabel>
#include <QLineEdit>
#include <QMessageBox>
#include <QPlainTextEdit>
#include <QPushButton>
#include <QRegularExpression>
#include <QSettings>
#include <QSpinBox>
#include <QTableWidget>
#include <QVBoxLayout>

#include <omegamaps/omegamaps.h>

#include "runbridge.h"
#include "theme.h"
#include "vault.h"
#include "vaultdialogs.h"

namespace omegamaps {

QStringList splitList(const QString &text) {
    static const QRegularExpression sep(QStringLiteral("[,;\\s]+"));
    return text.split(sep, Qt::SkipEmptyParts);
}

namespace {

// Caption, field, and an optional line of explanation under it: the sc2
// field shape.
void addField(QVBoxLayout *col, const QString &caption, QWidget *field, const QString &hint = QString()) {
    auto *box = new QVBoxLayout;
    box->setSpacing(4);
    box->addWidget(makeCaption(caption.toUpper(), "fieldLabel", 8, field->parentWidget()));
    box->addWidget(field);
    if (!hint.isEmpty()) {
        auto *h = new QLabel(hint, field->parentWidget());
        h->setProperty("tone", QStringLiteral("muted"));
        h->setWordWrap(true);
        box->addWidget(h);
    }
    col->addLayout(box);
}

QString expandHome(const QString &p) {
    if (p == QLatin1String("~")) return QDir::homePath();
    if (p.startsWith(QLatin1String("~/"))) return QDir::homePath() + p.mid(1);
    return p;
}

QJsonArray toArray(const QStringList &l) {
    QJsonArray a;
    for (const QString &s : l) a.append(s);
    return a;
}

// Method orders the form offers, as the request spells them.
const std::pair<const char *, QStringList> &methodChoice(int i) {
    static const std::pair<const char *, QStringList> choices[] = {
        {"SSH only", {QStringLiteral("ssh")}},
        {"SNMP only", {QStringLiteral("snmp")}},
        {"SNMP, then SSH", {QStringLiteral("snmp"), QStringLiteral("ssh")}},
        {"SSH, then SNMP", {QStringLiteral("ssh"), QStringLiteral("snmp")}},
    };
    return choices[std::clamp(i, 0, 3)];
}

}  // namespace

// ---------------------------------------------------------------------------

ConnectionPanel::ConnectionPanel(QWidget *parent) : Panel(QStringLiteral("Connection"), parent) {
    seeds = new QPlainTextEdit(this);
    seeds->setPlaceholderText(tr("172.16.1.2\none per line, or separated by commas"));
    seeds->setFixedHeight(64);
    seeds->setTabChangesFocus(true);
    addField(bodyLayout(), tr("Seed addresses"), seeds);

    domains = new QLineEdit(this);
    domains->setPlaceholderText(QStringLiteral("lab.local"));
    addField(bodyLayout(), tr("Domain suffixes"), domains,
             tr("Appended to a name that does not resolve, and stripped from names in the map."));

    allowDomains = new QLineEdit(this);
    allowDomains->setPlaceholderText(tr("(any)"));
    addField(bodyLayout(), tr("Dial only under"), allowDomains,
             tr("Neighbors outside these domains are mapped as leaves, never dialed. "
                "Worth setting when a seed faces a peering exchange."));

    exclude = new QLineEdit(this);
    exclude->setPlaceholderText(QStringLiteral("linux, phone"));
    addField(bodyLayout(), tr("Exclude"), exclude,
             tr("Substrings of a neighbor's platform, hostname, sysName or port description. "
                "Not globs: \"*phone*\" matches nothing."));
}

// ---------------------------------------------------------------------------

CredentialsPanel::CredentialsPanel(Vault *vault, QWidget *parent)
    : Panel(QStringLiteral("Credentials"), parent), m_vault(vault) {
    unlockBtn = new QPushButton(this);
    unlockBtn->setCursor(Qt::PointingHandCursor);
    barLayout()->addWidget(unlockBtn);
    connect(unlockBtn, &QPushButton::clicked, this, &CredentialsPanel::unlockOrLock);

    status = new QLabel(this);
    status->setProperty("tone", QStringLiteral("secondary"));
    status->setWordWrap(true);
    bodyLayout()->addWidget(status);

    table = new QTableWidget(0, 4, this);
    table->setHorizontalHeaderLabels({tr("NAME"), tr("TYPE"), tr("USER"), tr("TAGS")});
    table->horizontalHeader()->setStretchLastSection(true);
    table->horizontalHeader()->setSectionResizeMode(0, QHeaderView::ResizeToContents);
    table->horizontalHeader()->setSectionResizeMode(1, QHeaderView::ResizeToContents);
    table->verticalHeader()->hide();
    table->setEditTriggers(QAbstractItemView::NoEditTriggers);
    table->setSelectionBehavior(QAbstractItemView::SelectRows);
    table->setSelectionMode(QAbstractItemView::SingleSelection);
    table->setShowGrid(false);
    table->setMinimumHeight(120);
    bodyLayout()->addWidget(table, 1);

    auto *row = new QHBoxLayout;
    addBtn = new QPushButton(tr("+ Add"), this);
    removeBtn = new QPushButton(tr("Remove"), this);
    for (QPushButton *b : {addBtn, removeBtn}) b->setCursor(Qt::PointingHandCursor);
    row->addWidget(addBtn);
    row->addWidget(removeBtn);
    row->addStretch(1);
    bodyLayout()->addLayout(row);
    connect(addBtn, &QPushButton::clicked, this, &CredentialsPanel::addCredential);
    connect(removeBtn, &QPushButton::clicked, this, &CredentialsPanel::removeSelected);

    credTags = new QLineEdit(this);
    credTags->setPlaceholderText(tr("(every credential)"));
    addField(bodyLayout(), tr("Use credentials tagged"), credTags);

    connect(&ThemeManager::instance(), &ThemeManager::changed, this, &CredentialsPanel::refresh);
    refresh();
}

void CredentialsPanel::setVault(Vault *vault) {
    m_vault = vault;
    refresh();
}

void CredentialsPanel::refresh() {
    table->setRowCount(0);
    const bool open = m_vault && m_vault->isOpen();
    const bool exists = open && m_vault->exists();
    const bool unlocked = exists && !m_vault->isLocked();
    addBtn->setEnabled(unlocked);
    removeBtn->setEnabled(unlocked);

    if (!open) {
        status->setText(tr("No vault configured."));
        unlockBtn->setText(tr("Unlock\u2026"));
        unlockBtn->setEnabled(false);
        return;
    }
    unlockBtn->setEnabled(true);
    if (!exists) {
        status->setText(tr("No vault at %1 yet.").arg(m_vault->path()));
        setTone(status, QStringLiteral("warning"));
        unlockBtn->setText(tr("Create\u2026"));
        return;
    }
    if (!unlocked) {
        status->setText(tr("Locked: %1").arg(m_vault->path()));
        setTone(status, QStringLiteral("warning"));
        unlockBtn->setText(tr("Unlock\u2026"));
        return;
    }
    unlockBtn->setText(tr("Lock"));

    QVector<CredentialMeta> metas;
    if (m_vault->list(&metas) != VaultError::Ok) {
        status->setText(Vault::describe(VaultError::Internal, m_vault->lastError()));
        setTone(status, QStringLiteral("danger"));
        return;
    }
    status->setText((metas.size() == 1 ? tr("1 credential  \u00B7  %1")
                                        : tr("%1 credentials  \u00B7  %2").arg(metas.size()))
                        .arg(m_vault->path()));
    setTone(status, QStringLiteral("secondary"));

    const Tokens &t = ThemeManager::instance().tokens();
    table->setRowCount(int(metas.size()));
    for (int i = 0; i < metas.size(); ++i) {
        const CredentialMeta &m = metas.at(i);
        auto *name = new QTableWidgetItem(m.isDefault ? m.name + QStringLiteral(" \u2605") : m.name);
        name->setData(Qt::UserRole, m.id);
        if (m.disabled) name->setForeground(t.textMuted);
        auto *type = new QTableWidgetItem(m.isSnmp ? m.authLabel.toUpper() : QStringLiteral("SSH ") + m.authLabel);
        type->setForeground(m.isSnmp ? t.info : t.success);
        auto *user = new QTableWidgetItem(m.username.isEmpty() ? QStringLiteral("-") : m.username);
        auto *tags = new QTableWidgetItem(m.tags.join(QStringLiteral(", ")));
        table->setItem(i, 0, name);
        table->setItem(i, 1, type);
        table->setItem(i, 2, user);
        table->setItem(i, 3, tags);
    }
}

void CredentialsPanel::unlockOrLock() {
    if (!m_vault || !m_vault->isOpen()) return;
    if (m_vault->exists() && !m_vault->isLocked()) {
        m_vault->lock();
    } else {
        MasterPasswordDialog dlg(m_vault, window());
        dlg.exec();
    }
    refresh();
    emit vaultChanged();
}

void CredentialsPanel::addCredential() {
    CredentialDialog dlg(m_vault, window());
    if (dlg.exec() == QDialog::Accepted) {
        refresh();
        emit vaultChanged();
    }
}

void CredentialsPanel::removeSelected() {
    const auto rows = table->selectionModel() ? table->selectionModel()->selectedRows() : QModelIndexList{};
    if (rows.isEmpty()) return;
    QTableWidgetItem *item = table->item(rows.first().row(), 0);
    const QString id = item->data(Qt::UserRole).toString();
    if (QMessageBox::question(window(), tr("Remove credential"),
                              tr("Remove %1 from the vault?").arg(item->text())) != QMessageBox::Yes)
        return;
    m_vault->remove(id);
    refresh();
    emit vaultChanged();
}

// ---------------------------------------------------------------------------

OptionsPanel::OptionsPanel(QWidget *parent) : Panel(QStringLiteral("Discovery options"), parent) {
    auto spin = [this](int lo, int hi) {
        auto *s = new QSpinBox(this);
        s->setRange(lo, hi);
        // Keys and the wheel change it; the stock arrows do not take the
        // theme and render as dark blocks.
        s->setButtonSymbols(QAbstractSpinBox::NoButtons);
        return s;
    };
    depth = spin(0, 20);
    depth->setToolTip(tr("0 collects the seeds only"));
    concurrency = spin(1, 100);
    timeoutS = spin(1, 600);
    timeoutS->setSuffix(QStringLiteral(" s"));
    snmpTimeoutS = spin(0, 60);
    snmpTimeoutS->setSuffix(QStringLiteral(" s"));
    snmpTimeoutS->setSpecialValueText(tr("default"));
    snmpTimeoutS->setToolTip(tr("Per SNMP request, and the cost of every miss on a v2c ladder"));

    auto *grid = new QGridLayout;
    grid->setHorizontalSpacing(10);
    grid->setVerticalSpacing(4);
    const std::pair<const char *, QSpinBox *> spins[] = {
        {"Max depth", depth}, {"Concurrency", concurrency}, {"Timeout", timeoutS}};
    for (int i = 0; i < 3; ++i) {
        grid->addWidget(makeCaption(tr(spins[i].first).toUpper(), "fieldLabel", 8, this), 0, i);
        grid->addWidget(spins[i].second, 1, i);
    }
    bodyLayout()->addLayout(grid);

    methods = new QComboBox(this);
    for (int i = 0; i < 4; ++i) methods->addItem(tr(methodChoice(i).first));
    methods->setToolTip(tr("The order collection is tried on each device; the next is tried when one "
                           "fails or finds no neighbors"));
    addField(bodyLayout(), tr("Collect with"), methods);
    addField(bodyLayout(), tr("SNMP timeout"), snmpTimeoutS);

    hostKeys = new QComboBox(this);
    hostKeys->addItem(tr("Trust on first use, record the key"), QStringLiteral("tofu"));
    hostKeys->addItem(tr("Known hosts only"), QStringLiteral("strict"));
    addField(bodyLayout(), tr("SSH host keys"), hostKeys,
             tr("A key that changed is refused either way."));

    knownHosts = new QLineEdit(this);
    knownHosts->setPlaceholderText(QStringLiteral("~/.ssh/known_hosts"));
    addField(bodyLayout(), tr("Known hosts file"), knownHosts,
             tr("A separate file for a lab that gets rebuilt keeps its keys out of your own."));

    legacy = new QCheckBox(tr("Legacy SSH algorithms (older IOS, NX-OS)"), this);
    trustUni = new QCheckBox(tr("Trust one-sided links between discovered devices"), this);
    bodyLayout()->addWidget(legacy);
    bodyLayout()->addWidget(trustUni);
}

// ---------------------------------------------------------------------------

OutputPanel::OutputPanel(QWidget *parent) : Panel(QStringLiteral("Output"), parent) {
    directory = new QLineEdit(this);
    auto *browse = new QPushButton(tr("Browse\u2026"), this);
    browse->setCursor(Qt::PointingHandCursor);
    auto *row = new QHBoxLayout;
    row->setSpacing(6);
    row->addWidget(directory, 1);
    row->addWidget(browse);
    auto *box = new QVBoxLayout;
    box->setSpacing(4);
    box->addWidget(makeCaption(tr("OUTPUT DIRECTORY"), "fieldLabel", 8, this));
    box->addLayout(row);
    bodyLayout()->addLayout(box);
    connect(browse, &QPushButton::clicked, this, [this] {
        const QString d = pickDirectory(window(), tr("Output directory"), expandHome(directory->text()));
        if (!d.isEmpty()) directory->setText(d);
    });

    mapName = new QLineEdit(this);
    mapName->setPlaceholderText(QStringLiteral("network_map"));
    addField(bodyLayout(), tr("Map name"), mapName);

    resolved = new QLabel(this);
    resolved->setProperty("tone", QStringLiteral("muted"));
    resolved->setWordWrap(true);
    bodyLayout()->addWidget(resolved);

    connect(directory, &QLineEdit::textChanged, this, &OutputPanel::updateResolved);
    connect(mapName, &QLineEdit::textChanged, this, &OutputPanel::updateResolved);
    updateResolved();
}

QString OutputPanel::mapPath() const {
    const QString dir = expandHome(directory->text().trimmed());
    QString name = mapName->text().trimmed();
    if (dir.isEmpty() || name.isEmpty()) return QString();
    if (!name.endsWith(QLatin1String(".json"), Qt::CaseInsensitive)) name += QStringLiteral(".json");
    return QDir(dir).filePath(name);
}

void OutputPanel::updateResolved() {
    const QString p = mapPath();
    resolved->setText(p.isEmpty() ? tr("Needs a directory and a name.")
                                   : tr("\u2192 %1\nwith the recording and the crawl log beside it").arg(p));
}

// ---------------------------------------------------------------------------

RunPanel::RunPanel(QWidget *parent) : Panel(QStringLiteral("Run"), parent) {
    start = new QPushButton(tr("\u25B6  START CRAWL"), this);
    start->setProperty("primary", QStringLiteral("true"));
    start->setMinimumHeight(40);
    start->setCursor(Qt::PointingHandCursor);
    bodyLayout()->addWidget(start);

    auto *row = new QHBoxLayout;
    testSingle = new QPushButton(tr("Test single"), this);
    testSingle->setToolTip(tr("Collect the first seed only: depth 0, written as <map name>-test.json"));
    stop = new QPushButton(tr("Stop"), this);
    stop->setEnabled(false);
    viewMap = new QPushButton(tr("View map"), this);
    viewMap->setToolTip(tr("Open this run's map in the map viewer, with its exports"));
    viewMap->setEnabled(false);
    for (QPushButton *b : {testSingle, stop, viewMap}) {
        b->setCursor(Qt::PointingHandCursor);
        row->addWidget(b);
    }
    bodyLayout()->addLayout(row);

    status = new QLabel(tr("Ready"), this);
    status->setProperty("tone", QStringLiteral("muted"));
    status->setWordWrap(true);
    bodyLayout()->addWidget(status);

    problems = new QLabel(this);
    problems->setProperty("tone", QStringLiteral("danger"));
    problems->setWordWrap(true);
    problems->hide();
    bodyLayout()->addWidget(problems);
}

void RunPanel::setStatus(const QString &text, const QString &tone) {
    status->setText(text);
    setTone(status, tone);
}

// ---------------------------------------------------------------------------

CrawlForm::CrawlForm(Vault *vault, ConnectionPanel *conn, CredentialsPanel *creds, OptionsPanel *opts,
                     OutputPanel *out, RunPanel *run, QObject *parent)
    : QObject(parent), m_vault(vault), m_conn(conn), m_creds(creds), m_opts(opts), m_out(out), m_run(run) {
    m_fields = {
        {QStringLiteral("seeds"), conn->seeds},
        {QStringLiteral("domains"), conn->domains},
        {QStringLiteral("allow_domains"), conn->allowDomains},
        {QStringLiteral("exclude"), conn->exclude},
        {QStringLiteral("cred_tags"), creds->credTags},
        {QStringLiteral("credentials"), creds->status},
        {QStringLiteral("depth"), opts->depth},
        {QStringLiteral("concurrency"), opts->concurrency},
        {QStringLiteral("timeout_ms"), opts->timeoutS},
        {QStringLiteral("snmp_timeout_ms"), opts->snmpTimeoutS},
        {QStringLiteral("methods"), opts->methods},
        {QStringLiteral("host_keys"), opts->hostKeys},
        {QStringLiteral("known_hosts_path"), opts->knownHosts},
        {QStringLiteral("map_path"), out->mapName},
    };
    connect(run->start, &QPushButton::clicked, this, [this] { startClicked(false); });
    connect(run->testSingle, &QPushButton::clicked, this, [this] { startClicked(true); });
    connect(run->stop, &QPushButton::clicked, this, &CrawlForm::stopRequested);
    applyDefaults();
}

void CrawlForm::applyDefaults() {
    char *raw = omegamaps_crawl_defaults();
    const QJsonObject d = QJsonDocument::fromJson(QByteArray(raw ? raw : "{}")).object();
    omegamaps_free(raw);
    m_opts->depth->setValue(d.value(QLatin1String("depth")).toInt(3));
    m_opts->concurrency->setValue(d.value(QLatin1String("concurrency")).toInt(5));
    m_opts->timeoutS->setValue(int(d.value(QLatin1String("timeout_ms")).toDouble(30000) / 1000));
    m_opts->snmpTimeoutS->setValue(int(d.value(QLatin1String("snmp_timeout_ms")).toDouble(0) / 1000));
    QStringList methods;
    for (const QJsonValue &v : d.value(QLatin1String("methods")).toArray()) methods << v.toString();
    for (int i = 0; i < 4; ++i)
        if (methodChoice(i).second == methods) m_opts->methods->setCurrentIndex(i);
    const int hk = m_opts->hostKeys->findData(d.value(QLatin1String("host_keys")).toString());
    if (hk >= 0) m_opts->hostKeys->setCurrentIndex(hk);
    m_out->directory->setText(QDir::home().filePath(QStringLiteral("omegamaps/maps")));
    m_out->mapName->setText(QStringLiteral("network_map"));
}

QByteArray CrawlForm::request(bool testSingle) const {
    QJsonObject o;
    const QString seedText = m_conn->seeds->toPlainText();
    if (testSingle) {
        const QStringList seeds = splitList(seedText);
        o.insert(QStringLiteral("seeds"), toArray(seeds.isEmpty() ? QStringList{} : QStringList{seeds.first()}));
        o.insert(QStringLiteral("depth"), 0);
    } else {
        // One element: the library splits free text the way the command line does.
        o.insert(QStringLiteral("seeds"), toArray(seedText.trimmed().isEmpty() ? QStringList{} : QStringList{seedText}));
        o.insert(QStringLiteral("depth"), m_opts->depth->value());
    }
    o.insert(QStringLiteral("concurrency"), m_opts->concurrency->value());
    o.insert(QStringLiteral("timeout_ms"), m_opts->timeoutS->value() * 1000);
    if (m_opts->snmpTimeoutS->value() > 0)
        o.insert(QStringLiteral("snmp_timeout_ms"), m_opts->snmpTimeoutS->value() * 1000);
    o.insert(QStringLiteral("domains"), toArray(splitList(m_conn->domains->text())));
    o.insert(QStringLiteral("allow_domains"), toArray(splitList(m_conn->allowDomains->text())));
    o.insert(QStringLiteral("exclude"), toArray(splitList(m_conn->exclude->text())));
    o.insert(QStringLiteral("cred_tags"), toArray(splitList(m_creds->credTags->text())));
    o.insert(QStringLiteral("methods"), toArray(methodChoice(m_opts->methods->currentIndex()).second));
    o.insert(QStringLiteral("host_keys"), m_opts->hostKeys->currentData().toString());
    const QString kh = expandHome(m_opts->knownHosts->text().trimmed());
    if (!kh.isEmpty()) o.insert(QStringLiteral("known_hosts_path"), kh);
    o.insert(QStringLiteral("legacy"), m_opts->legacy->isChecked());
    o.insert(QStringLiteral("trust_unidirectional"), m_opts->trustUni->isChecked());
    QString map = m_out->mapPath();
    if (testSingle && !map.isEmpty()) map.replace(QRegularExpression(QStringLiteral("\\.json$")), QStringLiteral("-test.json"));
    o.insert(QStringLiteral("map_path"), map);
    return QJsonDocument(o).toJson(QJsonDocument::Compact);
}

void CrawlForm::clearMarks() {
    for (QWidget *w : std::as_const(m_fields)) {
        setStyleProperty(w, "invalid", QStringLiteral("false"));
        w->setToolTip(QString());
    }
    setStyleProperty(m_out->directory, "invalid", QStringLiteral("false"));
    m_run->problems->clear();
    m_run->problems->hide();
}

bool CrawlForm::validate(const QByteArray &req) {
    clearMarks();
    QVector<QPair<QString, QString>> problems = RunBridge::validateCrawl(req);
    // Credentials are the vault, not the request, so the library cannot see
    // this one before the crawl opens; saying it here beats a failed open.
    if (!m_vault || !m_vault->isOpen() || !m_vault->exists() || m_vault->isLocked())
        problems.append({QStringLiteral("credentials"), tr("unlock the credential vault first")});

    QStringList lines;
    for (const auto &[field, message] : problems) {
        if (QWidget *w = m_fields.value(field)) {
            setStyleProperty(w, "invalid", QStringLiteral("true"));
            w->setToolTip(message);
        }
        if (field == QLatin1String("map_path"))
            setStyleProperty(m_out->directory, "invalid", QStringLiteral("true"));
        lines << QStringLiteral("%1: %2").arg(field, message);
    }
    if (!lines.isEmpty()) {
        m_run->problems->setText(lines.join(QLatin1Char('\n')));
        m_run->problems->show();
    }
    return problems.isEmpty();
}

void CrawlForm::startClicked(bool testSingle) {
    const QByteArray req = request(testSingle);
    if (!validate(req)) return;
    save();
    emit startRequested(req);
}

void CrawlForm::setRunning(bool running) {
    m_run->start->setEnabled(!running);
    m_run->testSingle->setEnabled(!running);
    m_run->stop->setEnabled(running);
}

void CrawlForm::load() {
    applyDefaults();
    QSettings s;
    s.beginGroup(QStringLiteral("crawl"));
    auto str = [&](const char *key, QLineEdit *e) {
        if (s.contains(QLatin1String(key))) e->setText(s.value(QLatin1String(key)).toString());
    };
    if (s.contains(QStringLiteral("seeds"))) m_conn->seeds->setPlainText(s.value(QStringLiteral("seeds")).toString());
    str("domains", m_conn->domains);
    str("allow_domains", m_conn->allowDomains);
    str("exclude", m_conn->exclude);
    str("cred_tags", m_creds->credTags);
    str("known_hosts", m_opts->knownHosts);
    str("output_dir", m_out->directory);
    str("map_name", m_out->mapName);
    auto num = [&](const char *key, QSpinBox *sp) {
        if (s.contains(QLatin1String(key))) sp->setValue(s.value(QLatin1String(key)).toInt());
    };
    num("depth", m_opts->depth);
    num("concurrency", m_opts->concurrency);
    num("timeout_s", m_opts->timeoutS);
    num("snmp_timeout_s", m_opts->snmpTimeoutS);
    if (s.contains(QStringLiteral("methods"))) m_opts->methods->setCurrentIndex(s.value(QStringLiteral("methods")).toInt());
    if (s.contains(QStringLiteral("host_keys"))) {
        const int i = m_opts->hostKeys->findData(s.value(QStringLiteral("host_keys")).toString());
        if (i >= 0) m_opts->hostKeys->setCurrentIndex(i);
    }
    m_opts->legacy->setChecked(s.value(QStringLiteral("legacy"), false).toBool());
    m_opts->trustUni->setChecked(s.value(QStringLiteral("trust_unidirectional"), false).toBool());
}

void CrawlForm::save() const {
    QSettings s;
    s.beginGroup(QStringLiteral("crawl"));
    s.setValue(QStringLiteral("seeds"), m_conn->seeds->toPlainText());
    s.setValue(QStringLiteral("domains"), m_conn->domains->text());
    s.setValue(QStringLiteral("allow_domains"), m_conn->allowDomains->text());
    s.setValue(QStringLiteral("exclude"), m_conn->exclude->text());
    s.setValue(QStringLiteral("cred_tags"), m_creds->credTags->text());
    s.setValue(QStringLiteral("known_hosts"), m_opts->knownHosts->text());
    s.setValue(QStringLiteral("output_dir"), m_out->directory->text());
    s.setValue(QStringLiteral("map_name"), m_out->mapName->text());
    s.setValue(QStringLiteral("depth"), m_opts->depth->value());
    s.setValue(QStringLiteral("concurrency"), m_opts->concurrency->value());
    s.setValue(QStringLiteral("timeout_s"), m_opts->timeoutS->value());
    s.setValue(QStringLiteral("snmp_timeout_s"), m_opts->snmpTimeoutS->value());
    s.setValue(QStringLiteral("methods"), m_opts->methods->currentIndex());
    s.setValue(QStringLiteral("host_keys"), m_opts->hostKeys->currentData().toString());
    s.setValue(QStringLiteral("legacy"), m_opts->legacy->isChecked());
    s.setValue(QStringLiteral("trust_unidirectional"), m_opts->trustUni->isChecked());
}

}  // namespace omegamaps

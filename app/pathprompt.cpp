// app/pathprompt.cpp

#include "pathprompt.h"

#include <QCompleter>
#include <QDateTime>
#include <QDialogButtonBox>
#include <QDir>
#include <QDragEnterEvent>
#include <QDropEvent>
#include <QFileInfo>
#include <QFileSystemModel>
#include <QHBoxLayout>
#include <QLabel>
#include <QLineEdit>
#include <QLocale>
#include <QMimeData>
#include <QPushButton>
#include <QUrl>
#include <QVBoxLayout>

#include "filedialogs.h"
#include "theme.h"

namespace omegamaps {

PathPrompt::PathPrompt(QWidget *parent, const QString &title, const QString &label, const QString &filter)
    : QDialog(parent), m_filter(filter), m_check(&PathPrompt::mustBeFile) {
    setWindowTitle(title);
    setMinimumWidth(560);

    auto *col = new QVBoxLayout(this);
    col->setSpacing(8);
    auto *caption = new QLabel(label, this);
    col->addWidget(caption);

    auto *row = new QHBoxLayout;
    m_edit = new QLineEdit(this);
    m_edit->setPlaceholderText(tr("Type, paste or drop a path"));
    m_edit->setClearButtonEnabled(true);
    m_edit->installEventFilter(this);
    auto *model = new QFileSystemModel(this);
    model->setRootPath(QString());
    auto *completer = new QCompleter(model, this);
    completer->setCompletionMode(QCompleter::PopupCompletion);
    m_edit->setCompleter(completer);
    row->addWidget(m_edit, 1);
    auto *browseBtn = new QPushButton(tr("Browse\u2026"), this);
    browseBtn->setAutoDefault(false);
    row->addWidget(browseBtn);
    col->addLayout(row);

    m_hint = new QLabel(this);
    m_hint->setWordWrap(true);
    col->addWidget(m_hint);

    auto *buttons = new QDialogButtonBox(QDialogButtonBox::Open | QDialogButtonBox::Cancel, this);
    m_ok = buttons->button(QDialogButtonBox::Open);
    m_ok->setDefault(true);
    // The stylesheet's default-action button. Through setStyleProperty: the
    // button box has already sized, and so polished, its buttons.
    setStyleProperty(m_ok, "primary", QStringLiteral("true"));
    col->addWidget(buttons);

    connect(browseBtn, &QPushButton::clicked, this, &PathPrompt::browse);
    connect(m_edit, &QLineEdit::textChanged, this, &PathPrompt::revalidate);
    connect(buttons, &QDialogButtonBox::accepted, this, [this] {
        if (acceptable()) accept();
    });
    connect(buttons, &QDialogButtonBox::rejected, this, &QDialog::reject);
    revalidate();
}

QString PathPrompt::resolve(const QString &typed) {
    QString s = typed.trimmed();
    if (s.size() >= 2 && ((s.startsWith(QLatin1Char('"')) && s.endsWith(QLatin1Char('"'))) ||
                          (s.startsWith(QLatin1Char('\'')) && s.endsWith(QLatin1Char('\''))))) {
        s = s.mid(1, s.size() - 2);
    }
    if (s.startsWith(QLatin1String("file:"))) s = QUrl(s).toLocalFile();
    if (s == QLatin1String("~")) s = QDir::homePath();
    else if (s.startsWith(QLatin1String("~/"))) s = QDir::homePath() + s.mid(1);
    if (s.isEmpty()) return QString();
    return QDir::cleanPath(QFileInfo(s).absoluteFilePath());
}

QString PathPrompt::mustBeFile(const QString &path) {
    const QFileInfo fi(path);
    if (!fi.exists()) return tr("No such file");
    if (!fi.isFile()) return tr("Not a file");
    if (!fi.isReadable()) return tr("Not readable");
    return QString();
}

void PathPrompt::setPath(const QString &text) { m_edit->setText(text); }
QString PathPrompt::path() const { return resolve(m_edit->text()); }
bool PathPrompt::acceptable() const { return m_acceptable; }
QString PathPrompt::hint() const { return m_hint->text(); }

void PathPrompt::setCheck(Check check) {
    m_check = check ? std::move(check) : Check(&PathPrompt::mustBeFile);
    revalidate();
}

void PathPrompt::revalidate() {
    const QString p = path();
    QString why = p.isEmpty() ? QString() : m_check(p);
    m_acceptable = !p.isEmpty() && why.isEmpty();
    m_ok->setEnabled(m_acceptable);
    if (p.isEmpty()) {
        m_hint->setText(QString());
    } else if (!why.isEmpty()) {
        m_hint->setText(why);
        setTone(m_hint, QStringLiteral("danger"));
    } else {
        const QFileInfo fi(p);
        m_hint->setText(fi.isDir() ? QDir::toNativeSeparators(p)
                                   : tr("%1  \u00B7  modified %2")
                                         .arg(QLocale().formattedDataSize(fi.size()),
                                              QLocale().toString(fi.lastModified(), QLocale::ShortFormat)));
        setTone(m_hint, QStringLiteral("muted"));
    }
}

void PathPrompt::browse() {
    const QString current = path();
    const QString start = QFileInfo(current).isDir() ? current : QFileInfo(current).absolutePath();
    const QString got = pickOpenFile(this, windowTitle(), current.isEmpty() ? QString() : start, m_filter);
    if (!got.isEmpty()) setPath(QDir::toNativeSeparators(got));
}

// A file dragged in from the file manager becomes its path, whatever the
// field would otherwise have done with the drop.
bool PathPrompt::eventFilter(QObject *watched, QEvent *event) {
    if (watched == m_edit) {
        if (event->type() == QEvent::DragEnter) {
            auto *e = static_cast<QDragEnterEvent *>(event);
            if (e->mimeData()->hasUrls()) {
                e->acceptProposedAction();
                return true;
            }
        } else if (event->type() == QEvent::Drop) {
            auto *e = static_cast<QDropEvent *>(event);
            const QList<QUrl> urls = e->mimeData()->urls();
            if (!urls.isEmpty() && urls.first().isLocalFile()) {
                setPath(QDir::toNativeSeparators(urls.first().toLocalFile()));
                e->acceptProposedAction();
                return true;
            }
        }
    }
    return QDialog::eventFilter(watched, event);
}

QString PathPrompt::getPath(QWidget *parent, const QString &title, const QString &label, const QString &initial,
                            const QString &filter, Check check) {
    PathPrompt p(parent, title, label, filter);
    if (check) p.setCheck(std::move(check));
    p.setPath(QDir::toNativeSeparators(initial));
    return p.exec() == QDialog::Accepted ? p.path() : QString();
}

}  // namespace omegamaps

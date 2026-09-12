// app/pathprompt.h
//
// "Which file?" as a path field first and a picker second. The path can be
// typed (with completion), pasted -- ~ expanded, quotes and file:// dropped --
// or dragged in from the file manager; Browse... opens the picker, through
// filedialogs.h. OK is enabled only when the path passes the check, and the
// line under the field says why not, or what the file is.
//
// It exists because a file picker is the one part of opening a file that can
// fail with no sign at all (filedialogs.h); a text field cannot.

#ifndef OMEGAMAPS_APP_PATHPROMPT_H
#define OMEGAMAPS_APP_PATHPROMPT_H

#include <QDialog>

#include <functional>

class QLabel;
class QLineEdit;
class QPushButton;

namespace omegamaps {

class PathPrompt : public QDialog {
    Q_OBJECT
public:
    // Given a resolved path: "" when it will do, otherwise why not.
    using Check = std::function<QString(const QString &path)>;

    PathPrompt(QWidget *parent, const QString &title, const QString &label, const QString &filter);

    void setPath(const QString &text);
    QString path() const;  // resolved, see resolve()
    void setCheck(Check check);
    bool acceptable() const;
    QString hint() const;

    // What was typed, as a path: whitespace and surrounding quotes trimmed,
    // a file:// URL made local, a leading ~ expanded, made absolute.
    static QString resolve(const QString &typed);

    // The default check: an existing file.
    static QString mustBeFile(const QString &path);

    // Asks, modally; "" when cancelled.
    static QString getPath(QWidget *parent, const QString &title, const QString &label, const QString &initial,
                           const QString &filter, Check check = Check());

protected:
    bool eventFilter(QObject *watched, QEvent *event) override;

private:
    void browse();
    void revalidate();

    QLineEdit *m_edit = nullptr;
    QLabel *m_hint = nullptr;
    QPushButton *m_ok = nullptr;
    QString m_filter;
    Check m_check;
    bool m_acceptable = false;
};

}  // namespace omegamaps

#endif

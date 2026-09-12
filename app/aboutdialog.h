// app/aboutdialog.h
//
// The About box: the splash, the version and what it runs on. Modeless and
// one at a time -- a second click raises the one already open. Both programs
// show it: the application from its wordmark, the viewer from Help.
//
// The splash is a PNG (256 colours, visually the same as the original JPEG
// at about the same size): PNG is built into QtGui, where JPEG is an
// image-format plugin, and the application is kept free of those so a bundle
// needs none (app/CMakeLists.txt).

#ifndef OMEGAMAPS_APP_ABOUTDIALOG_H
#define OMEGAMAPS_APP_ABOUTDIALOG_H

#include <QDialog>

class QLabel;
class QWidget;

namespace omegamaps {

class AboutDialog : public QDialog {
    Q_OBJECT
public:
    // Opens the About box over parent, or raises the one already open.
    static AboutDialog *showFor(QWidget *parent);

    // The version, Qt and platform lines, as copied by Copy details.
    QString details() const { return m_details; }
    const QLabel *splash() const { return m_splash; }

    // Makes w open the About box when clicked: a pointing cursor, a tooltip,
    // and a click handler. For a label that is not a button.
    static void makeTrigger(QWidget *w);

private:
    explicit AboutDialog(QWidget *parent);

    QLabel *m_splash = nullptr;
    QString m_details;
};

}  // namespace omegamaps

#endif

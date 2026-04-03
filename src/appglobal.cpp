#include "appglobal.h"
#include "logging.h"
#include "soundbase.h"
#include <QSettings>

#ifndef HEADLESS
#include <QPixmap>
#include <QCursor>
#endif


const QString MAJORVERSION  = "9.5";
const QString CONFIGVERSION = "9.0";
const QString MINORVERSION  = ".11";
const QString LOGVERSION = ("qsstv."+MAJORVERSION+MINORVERSION+".log");
const QString ORGANIZATION = "ON4QZ";
const QString APPLICATION  = ("qsstv_" +CONFIGVERSION);
const QString qsstvVersion=QString("QSSTV " + MAJORVERSION+MINORVERSION);
const QString APPNAME=QString("QSSTV");


// Globals shared by both GUI and headless builds
soundBase *soundIOPtr;
logFile *logFilePtr;

ftpThread *notifyRXIntfPtr;
ftpThread *hybridTxIntfPtr;
ftpThread *notifyTXIntfPtr;
ftpThread *onlineStatusIntfPtr;
ftpThread *hybridRxIntfPtr;
ftpThread *saveImageIntfPtr;

fileWatcher *fileWatcherPtr;

int fftNumBlocks=2;
bool useHybrid;
bool inStartup;

etransmissionMode transmissionModeIndex;  // SSTV , DRM


#ifdef HEADLESS
// ---------------------------------------------------------------------------
// Headless-only globals
// ---------------------------------------------------------------------------
#include "headlesscontroller.h"
#include "headlessrxcontroller.h"

QObject              *dispatcherPtr    = nullptr;
headlessController   *headlessCtrlPtr  = nullptr;
headlessRxController *rxWidgetPtr      = nullptr;

#else
// ---------------------------------------------------------------------------
// GUI-only globals
// ---------------------------------------------------------------------------
QSplashScreen *splashPtr;
QString splashStr;

mainWindow *mainWindowPtr;
configDialog *configDialogPtr;

dispatcher *dispatcherPtr;
QStatusBar *statusBarPtr;
rxWidget *rxWidgetPtr;
txWidget *txWidgetPtr;
galleryWidget *galleryWidgetPtr;
waterfallText *waterfallPtr;
rigControl *rigControllerPtr;
xmlInterface *xmlIntfPtr;
logBook *logBookPtr;

QPixmap *greenPXMPtr;
QPixmap *redPXMPtr;

#ifndef QT_NO_DEBUG
scopeView *scopeViewerData;
scopeView *scopeViewerSyncNarrow;
scopeView *scopeViewerSyncWide;
#endif

#endif // HEADLESS


void globalInit()
{
  logFilePtr=new logFile();
  logFilePtr->open(LOGVERSION);
  QSettings qSettings;
  qSettings.beginGroup("MAIN");
  logFilePtr->readSettings();
#ifndef HEADLESS
  greenPXMPtr=new QPixmap(16,16);
  greenPXMPtr->fill(Qt::green);
  redPXMPtr=new QPixmap(16,16);
  redPXMPtr->fill(Qt::red);
#endif
  qSettings.endGroup();
}

void globalEnd(void)
{
  logFilePtr->writeSettings();
  logFilePtr->close();
}

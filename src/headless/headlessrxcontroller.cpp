#include "headlessrxcontroller.h"
#include "rxfunctions.h"
#include "sstvrx.h"
#include "appglobal.h"
#include "sstvparam.h"

#include <QDateTime>
#include <QDir>
#include <QRegExp>
#include <QDebug>

// Access the global defaultImageFormat without pulling in guiconfig.h
// (which inherits QWidget via baseConfig).  The symbol is defined in
// src/config/guiconfig.cpp and will be linked in regardless of build mode.
extern QString defaultImageFormat;

headlessRxController::headlessRxController(const QString &outputDir,
                                             const QString &freqLabel,
                                             QObject *parent)
  : QObject(parent)
  , rxFunctionsPtr(nullptr)
  , outputDir(outputDir)
  , freqLabel(freqLabel)
{
}

headlessRxController::~headlessRxController()
{
  if (rxFunctionsPtr) {
    rxFunctionsPtr->stopThread();
    delete rxFunctionsPtr;
  }
}

void headlessRxController::init()
{
  rxFunctionsPtr = new rxFunctions(this);
  // Initialise the SSTV decoder filters synchronously on the calling thread
  // before starting the RX thread.  rxFunctions::init() only sets rxState=RXINIT
  // and relies on the thread to call forceInit() asynchronously — but startRX()
  // may fire before the thread processes RXINIT, leaving videoFilterPtr NULL and
  // causing sstvRx::run() to silently return on every call.  Calling init()
  // directly here guarantees filters are ready before any audio is processed.
  rxFunctionsPtr->sstvRxPtr->init();
  rxFunctionsPtr->start();  // launch the RX processing thread
}

void headlessRxController::createImage(QSize sz, QColor bg)
{
  imageBuffer.createImage(sz, bg, false);
  qInfo() << "headlessRxController: new image" << sz.width() << "x" << sz.height();
}

QString headlessRxController::saveCurrentImageAndGetPath(esstvMode mode, const QString &callsign)
{
  const QImage &img = imageBuffer.getImage();
  if (img.isNull()) {
    qWarning() << "headlessRxController: saveCurrentImage called with null image";
    return QString();
  }

  // Ensure output directory exists
  QDir dir(outputDir);
  if (!dir.exists()) {
    if (!dir.mkpath(outputDir)) {
      qCritical() << "headlessRxController: cannot create output directory" << outputDir;
      return QString();
    }
  }

  QString filename = buildFilename(mode, callsign);
  QString fullPath = outputDir + QDir::separator() + filename;

  // Use defaultImageFormat from guiConfig globals (set by headlessController::loadConfig)
  // Fall back to "png" if not set
  QString fmt = defaultImageFormat.isEmpty() ? "png" : defaultImageFormat.toLower();

  if (img.save(fullPath, fmt.toUpper().toLatin1().data())) {
    qInfo() << "headlessRxController: saved" << fullPath;
    emit imageSaved(fullPath);
    return fullPath;
  } else {
    qCritical() << "headlessRxController: failed to save" << fullPath;
    return QString();
  }
}

void headlessRxController::saveCurrentImage(esstvMode mode, const QString &callsign)
{
  saveCurrentImageAndGetPath(mode, callsign);
}

void headlessRxController::changeTransmissionMode(int /*mode*/)
{
  // In headless mode we accept whatever mode the decoder detects
}

QString headlessRxController::buildFilename(esstvMode mode, const QString &callsign) const
{
  QString modeStr;
  if (mode == NOTVALID) {
    modeStr = "UNKNOWN";
  } else {
    modeStr = getSSTVModeNameShort(mode);
    if (modeStr.isEmpty()) modeStr = QString::number(static_cast<int>(mode));
  }

  QString timestamp = QDateTime::currentDateTimeUtc().toString("yyyyMMdd_HHmmss");
  QString fmt = defaultImageFormat.isEmpty() ? "png" : defaultImageFormat.toLower();

  // Build base name: [freqLabel_]MODE_TIMESTAMP[_CALLSIGN].ext
  QStringList parts;
  if (!freqLabel.isEmpty())
    parts << freqLabel;
  parts << modeStr << timestamp;
  if (!callsign.isEmpty()) {
    QString safeCall = callsign;
    safeCall.replace(QRegExp("[^A-Za-z0-9]"), "_");
    parts << safeCall;
  }

  return parts.join("_") + "." + fmt;
}

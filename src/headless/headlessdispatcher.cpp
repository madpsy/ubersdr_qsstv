#include "headlessdispatcher.h"
#include "headlessrxcontroller.h"
#include "headlessevents.h"
#include "appglobal.h"
#include "sstvparam.h"
#include "dispatchevents.h"
#include "soundbase.h"
#include "sstvrx.h"
#include <QDebug>
#include <QJsonObject>
#include <QDateTime>
#include <QBuffer>
#include <QByteArray>
#include <QImage>

// Avoid pulling in the QWidget-based guiConfig class; declare only what we need.
extern QColor imageBackGroundColor;

headlessDispatcher::headlessDispatcher(headlessRxController *rxCtrl, QObject *parent)
  : QObject(parent)
  , rxCtrl(rxCtrl)
{
}

headlessDispatcher::~headlessDispatcher()
{
}

void headlessDispatcher::init()
{
  // Wire FSK-ID callsign signal from the wide sync processor to this dispatcher
  // so that decoded callsigns are captured and included in saved image filenames.
  // (Mirrors the connect() in rxWidget::init() in the GUI build.)
  connect(&rxCtrl->functionsPtr()->sstvRxPtr->syncWideProc,
          SIGNAL(callReceived(QString)),
          this,
          SLOT(slotCallReceived(QString)));

  qInfo() << "headlessDispatcher initialised";
}

void headlessDispatcher::startRX()
{
  soundIOPtr->startCapture();
  rxCtrl->functionsPtr()->startRX();
}

void headlessDispatcher::idleAll()
{
  rxCtrl->functionsPtr()->stopAndWait();
}

void headlessDispatcher::logSSTV(const QString &call, bool fromFSKID)
{
  if (fromFSKID)
  {
    lastCallsign = call;
    qInfo() << "headlessDispatcher: FSK-ID callsign received:" << call;
    // Notify the web UI immediately so it can show the callsign while the
    // image is still being decoded.
    QJsonObject j;
    j["event"]    = "rx_callsign";
    j["callsign"] = call;
    j["timestamp"] = QDateTime::currentDateTimeUtc().toString(Qt::ISODate);
    writeHeadlessEvent(j);
  }
}

void headlessDispatcher::slotCallReceived(const QString &call)
{
  logSSTV(call, true);  // sets lastCallsign, emits rx_callsign event

  // If a deferred save is pending (image received, waiting for FSK-ID trailer),
  // cancel the timer and save immediately now that the callsign has arrived.
  if (saveTimer && saveTimer->isActive())
  {
    saveTimer->stop();
    slotSavePending();
  }
}

void headlessDispatcher::slotSavePending()
{
  // FSK-ID wait window has elapsed (or was cancelled early by slotCallReceived).
  // Save with whatever callsign arrived in the window.
  esstvMode modeToSave = pendingMode;
  pendingMode = NOTVALID;

  QString callsignForEvent = lastCallsign;
  QString savedPath = rxCtrl->saveCurrentImageAndGetPath(modeToSave, lastCallsign);
  lastCallsign.clear();

  QJsonObject j;
  j["event"]     = "rx_saved";
  j["timestamp"] = QDateTime::currentDateTimeUtc().toString(Qt::ISODate);
  j["file"]      = savedPath;
  j["callsign"]  = callsignForEvent;
  j["sstv_mode"] = getSSTVModeNameShort(modeToSave);
  writeHeadlessEvent(j);
}

void headlessDispatcher::saveRxSSTVImage(esstvMode /*mode*/)
{
  // No longer used — endSSTVImageRX now calls saveCurrentImageAndGetPath directly.
}

void headlessDispatcher::customEvent(QEvent *e)
{
  switch (static_cast<dispatchEventType>(e->type()))
  {
    // ── Spectrum / signal-quality displays — no UI, drop silently ──────────
    case displayFFT:
      break;

    case displaySync:
      break;

    case displayDRMStat:
      break;

    case displayDRMInfo:
      break;

    // ── RX status messages — log to console ────────────────────────────────
    case rxSSTVStatus:
    {
      auto *ev = static_cast<rxSSTVStatusEvent *>(e);
      if (ev->getStr() == "No sync")
      {
        // Print a heartbeat every 300 "No sync" events (~30 s at normal rate)
        // so the user knows the decoder is alive and listening.
        static int noSyncCount = 0;
        if (++noSyncCount % 300 == 0)
          qInfo() << "RX SSTV: listening... (no signal)";
      }
      else
      {
        qInfo() << "RX SSTV status:" << ev->getStr();
      }
      break;
    }

    case rxDRMStatus:
    {
      auto *ev = static_cast<rxDRMStatusEvent *>(e);
      qInfo() << "RX DRM status:" << ev->getStr();
      break;
    }

    // ── Image RX lifecycle ─────────────────────────────────────────────────
    case startImageRX:
    {
      // Flush any pending deferred save from the previous image before
      // resetting state for the new one.  This handles back-to-back
      // transmissions where no FSK-ID was sent (or it never arrived).
      if (saveTimer && saveTimer->isActive())
      {
        saveTimer->stop();
        slotSavePending();
      }

      auto *ev = static_cast<startImageRXEvent *>(e);
      rxCtrl->createImage(ev->getSize(), imageBackGroundColor);
      lineDisplayCounter = 0;  // reset partial-preview counter for new image
      lastCallsign.clear();    // new image, new callsign window
      qInfo() << "headlessDispatcher: startImageRX size=" << ev->getSize()
              << "mode=" << getSSTVModeNameShort(ev->getMode());
      {
        QJsonObject j;
        j["event"]     = "rx_start";
        j["timestamp"] = QDateTime::currentDateTimeUtc().toString(Qt::ISODate);
        j["width"]     = ev->getSize().width();
        j["height"]    = ev->getSize().height();
        j["sstv_mode"] = getSSTVModeNameShort(ev->getMode());
        // Include the known total transmission duration so the web UI can show
        // an accurate countdown rather than estimating from per-line timing.
        esstvMode m = ev->getMode();
        if (m != NOTVALID && m < NUMSSTVMODES)
        {
          double imageTimeSec = SSTVTable[m].imageTime;
          if (imageTimeSec > 0.0)
            j["image_time_ms"] = static_cast<int>(imageTimeSec * 1000.0 + 0.5);
        }
        writeHeadlessEvent(j);
      }
      break;
    }

    case lineDisplay:
    {
      // Emit a partial-image preview every RX_LINE_EMIT_INTERVAL lines so the
      // web UI can show the image building up in real time.
      auto *ev = static_cast<lineDisplayEvent *>(e);
      uint lineNbr = 0;
      ev->getInfo(lineNbr);
      lineDisplayCounter++;
      if (lineDisplayCounter % RX_LINE_EMIT_INTERVAL == 0)
      {
        const QImage &img = rxCtrl->getImageViewerPtr()->getImage();
        if (!img.isNull())
        {
          QByteArray jpegData;
          QBuffer buf(&jpegData);
          buf.open(QIODevice::WriteOnly);
          // Encode only the decoded rows so far to avoid sending a huge blank image.
          // Crop to the lines received so far (lineNbr+1 rows).
          int rows = static_cast<int>(lineNbr) + 1;
          if (rows > img.height()) rows = img.height();
          QImage partial = img.copy(0, 0, img.width(), rows);
          partial.save(&buf, "JPEG", 70);
          buf.close();
          QString b64 = QString::fromLatin1(jpegData.toBase64());
          QJsonObject j;
          j["event"]    = "rx_line";
          j["line"]     = static_cast<int>(lineNbr);
          j["total"]    = img.height();
          j["jpeg_b64"] = b64;
          writeHeadlessEvent(j);
        }
      }
      break;
    }

    case endSSTVImageRX:
    {
      auto *ev = static_cast<endImageSSTVRXEvent *>(e);
      if (ev->getMode() == NOTVALID)
      {
        qInfo() << "headlessDispatcher: image below minCompletion threshold, discarding";
        {
          QJsonObject j;
          j["event"]     = "rx_discarded";
          j["timestamp"] = QDateTime::currentDateTimeUtc().toString(Qt::ISODate);
          j["reason"]    = "below_min_completion";
          writeHeadlessEvent(j);
        }
        lastCallsign.clear();
        break;
      }
      qInfo() << "headlessDispatcher: endSSTVImageRX mode=" << static_cast<int>(ev->getMode())
              << "— arming FSK-ID wait timer (" << FSK_ID_WAIT_MS << "ms)";
      // Arm a short timer to wait for the FSK-ID trailer.
      // The FSK-ID is transmitted *after* the image in the standard SSTV
      // protocol, so it arrives 100–500 ms after endSSTVImageRX fires.
      // slotSavePending() will fire after FSK_ID_WAIT_MS, or sooner if
      // slotCallReceived() arrives first and cancels + fires it immediately.
      pendingMode = ev->getMode();
      if (!saveTimer)
      {
        saveTimer = new QTimer(this);
        saveTimer->setSingleShot(true);
        connect(saveTimer, &QTimer::timeout, this, &headlessDispatcher::slotSavePending);
      }
      saveTimer->start(FSK_ID_WAIT_MS);
      break;
    }

    case saveDRMImage:
    {
      auto *ev = static_cast<saveDRMImageEvent *>(e);
      QString fn, info;
      ev->getFilename(fn);
      ev->getInfo(info);
      qInfo() << "headlessDispatcher: saveDRMImage file=" << fn << "info=" << info;
      // DRM images are already written to disk by the DRM decoder; just log.
      break;
    }

    case loadRXImage:
    {
      auto *ev = static_cast<loadRXImageEvent *>(e);
      qInfo() << "headlessDispatcher: loadRXImage file=" << ev->getFilename();
      break;
    }

    // ── General status / text ──────────────────────────────────────────────
    case statusBarMsg:
    {
      auto *ev = static_cast<statusBarMsgEvent *>(e);
      qInfo() << "Status:" << ev->getStr();
      break;
    }

    case displayText:
    {
      auto *ev = static_cast<displayTextEvent *>(e);
      qInfo() << "Text received:" << ev->getStr();
      break;
    }

    case displayMBox:
    {
      auto *ev = static_cast<displayMBoxEvent *>(e);
      qWarning() << "MBox [" << ev->getTitle() << "]:" << ev->getStr();
      break;
    }

    // ── TX events — no transmitter in headless RX mode, drop silently ──────
    case progressTX:
      break;

    case stoppingTX:
      break;

    case endImageTX:
      break;

    case txDRMNotify:
      break;

    case txDRMNotifyAppend:
      break;

    case txPrepareComplete:
      break;

    case moveToTx:
      break;

    // ── Editor / template events — no editor in headless mode ─────────────
    case callEditor:
      break;

    case editorFinished:
      break;

    case templatesChanged:
      break;

    // ── FTP / notify events — not used in headless mode ───────────────────
    case displayProgressFTP:
      break;

    case notifyCheck:
      break;

    case ftpSetup:
      break;

    case ftpUploadFile:
      break;

    // ── Soundcard idle ─────────────────────────────────────────────────────
    case soundcardIdle:
      qInfo() << "headlessDispatcher: soundcard idle";
      break;

    // ── Miscellaneous events that need no action in headless mode ──────────
    case info:
    {
      auto *ev = static_cast<infoEvent *>(e);
      qInfo() << "headlessDispatcher info:" << ev->getStr();
      break;
    }

    case syncDisp:
      break;

    case eraseDisp:
      break;

    case createMode:
      break;

    case outOfSync:
      break;

    case closeWindows:
      break;

    case changeRXFilter:
      break;

    case stopRxTx:
      break;

    case prepareFix:
      break;

    // ── Catch-all for any future event types ──────────────────────────────
    default:
      qWarning() << "headlessDispatcher: unhandled event type" << e->type();
      break;
  }

  // Signal to any waiting thread that the event has been processed.
  static_cast<baseEvent *>(e)->setDone();
}

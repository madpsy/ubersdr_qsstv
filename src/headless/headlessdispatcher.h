#ifndef HEADLESSDISPATCHER_H
#define HEADLESSDISPATCHER_H

#include <QObject>
#include <QTimer>
#include "dispatchevents.h"
#include "rxfunctions.h"

class headlessRxController;

/**
 * @brief Stripped dispatcher for headless RX-only mode.
 *
 * Handles only the dispatch events needed to receive and save images.
 * All TX, editor, gallery, spectrum, and UI events are silently dropped.
 *
 * Replaces dispatcher when HEADLESS is defined.
 */
class headlessDispatcher : public QObject
{
  Q_OBJECT

public:
  explicit headlessDispatcher(headlessRxController *rxCtrl, QObject *parent = nullptr);
  ~headlessDispatcher();

  void init();
  void startRX();
  void idleAll();

  // Called by dispatcher::logSSTV equivalent — tracks callsign for filename
  void logSSTV(const QString &call, bool fromFSKID);

public slots:
  // Connected to syncWideProc::callReceived to capture FSK-ID callsigns.
  // If a deferred save is pending, cancel the timer and save immediately
  // now that the callsign has arrived.
  void slotCallReceived(const QString &call);

  void customEvent(QEvent *e) override;

private slots:
  // Fired by saveTimer after FSK_ID_WAIT_MS — performs the actual image save.
  void slotSavePending();

private:
  headlessRxController *rxCtrl;

  // lastCallsign is the callsign that will be used when the image is saved.
  // It is populated either from the FSK-ID preamble (via preambleCallsign,
  // transferred at startImageRX time) or from the FSK-ID trailer (set
  // directly in slotCallReceived when a deferred save is pending).
  QString lastCallsign;

  // preambleCallsign holds a callsign decoded from the FSK-ID preamble that
  // arrived *before* the image started.  It is transferred into lastCallsign
  // at startImageRX time and cleared, so it survives the lastCallsign.clear()
  // that used to discard it.
  QString preambleCallsign;

  // Deferred-save state: set on endSSTVImageRX, consumed by slotSavePending().
  // Allows the FSK-ID trailer (which arrives after the image) to be captured
  // before the file is written and the rx_saved JSON event is emitted.
  esstvMode pendingMode = NOTVALID;
  QTimer   *saveTimer   = nullptr;

  // How long to wait for an FSK-ID trailer after the image ends.
  // Standard FSK-ID is ~500 ms; 3 s gives plenty of margin.
  static constexpr int FSK_ID_WAIT_MS = 3000;

  // Live preview: emit a partial JPEG every this many decoded lines.
  // At ~256 lines per image this gives ~16 updates per image.
  static constexpr int RX_LINE_EMIT_INTERVAL = 4;
  int lineDisplayCounter = 0;  // reset on each startImageRX

  void saveRxSSTVImage(esstvMode mode);
};

#endif // HEADLESSDISPATCHER_H

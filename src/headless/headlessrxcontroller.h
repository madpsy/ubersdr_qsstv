#ifndef HEADLESSRXCONTROLLER_H
#define HEADLESSRXCONTROLLER_H

#include <QObject>
#include <QString>
#include <QSize>
#include <QColor>

#include "headlessimagebuffer.h"
#include "sstvparam.h"

class rxFunctions;

/**
 * @brief RX controller for headless mode.
 *
 * Owns the rxFunctions processing thread and the current decoded image.
 * In headless mode appglobal.h declares rxWidgetPtr as headlessRxController*,
 * so modebase.cpp calls rxWidgetPtr->getImageViewerPtr()->getScanLineAddress()
 * directly on this concrete type — no inheritance from rxWidget needed.
 *
 * @param outputDir   Directory where decoded images are saved.
 * @param freqLabel   Frequency label prepended to saved filenames,
 *                    e.g. "14230_usb".  Empty = no prefix.
 */
class headlessRxController : public QObject
{
  Q_OBJECT

public:
  explicit headlessRxController(const QString &outputDir,
                                  const QString &freqLabel = QString(),
                                  QObject *parent = nullptr);
  ~headlessRxController();

  void init();

  // Called by headlessDispatcher on startImageRX event
  void createImage(QSize sz, QColor bg);

  // Interface used by modebase.cpp via rxWidgetPtr->getImageViewerPtr()
  headlessImageBuffer *getImageViewerPtr() { return &imageBuffer; }

  // Interface used by headlessDispatcher via rxCtrl->functionsPtr()
  rxFunctions *functionsPtr() { return rxFunctionsPtr; }

  // Called by headlessDispatcher on mode change events
  void changeTransmissionMode(int mode);

  /**
   * @brief Save the current decoded image and return the full path.
   * @param mode      Decoded SSTV mode.
   * @param callsign  FSK-ID callsign (may be empty).
   * @return Full path of the saved file, or empty string on failure.
   */
  QString saveCurrentImageAndGetPath(esstvMode mode, const QString &callsign);

  /** @deprecated Use saveCurrentImageAndGetPath instead. */
  void saveCurrentImage(esstvMode mode, const QString &callsign);

signals:
  void imageSaved(const QString &path);

private:
  QString buildFilename(esstvMode mode, const QString &callsign) const;

  rxFunctions        *rxFunctionsPtr;
  headlessImageBuffer imageBuffer;
  QString             outputDir;
  QString             freqLabel;
};

#endif // HEADLESSRXCONTROLLER_H

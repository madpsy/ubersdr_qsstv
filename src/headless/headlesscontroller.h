#ifndef HEADLESSCONTROLLER_H
#define HEADLESSCONTROLLER_H

#include <QObject>
#include <QString>

class headlessRxController;
class headlessDispatcher;

/**
 * @brief Top-level controller for headless mode. Replaces mainWindow.
 *
 * Initialises all subsystems, loads config from QSettings directly
 * (without instantiating any QWidget-based config dialog), and starts
 * the RX pipeline.
 *
 * @param outputDir   Directory where decoded images are saved.
 * @param eventsFd    File descriptor for JSON event output (--events-fd),
 *                    or -1 to disable structured event output.
 * @param freqLabel   Frequency label prepended to saved filenames
 *                    (--freq-label), e.g. "14230_usb".  Empty = no prefix.
 */
class headlessController : public QObject
{
  Q_OBJECT

public:
  explicit headlessController(const QString &outputDir,
                               int eventsFd = -1,
                               const QString &freqLabel = QString(),
                               QObject *parent = nullptr);
  ~headlessController();

  void init();
  void startRunning();

private:
  void loadConfig();

  QString outputDir;
  int     eventsFd;
  QString freqLabel;
  headlessRxController *rxCtrl;
  headlessDispatcher   *dispatcher;
};

#endif // HEADLESSCONTROLLER_H

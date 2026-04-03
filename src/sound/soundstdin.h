#ifndef SOUNDSTDIN_H
#define SOUNDSTDIN_H
#include "soundbase.h"
#include <QFile>

/**
 * @brief soundBase subclass that reads raw S16LE PCM from stdin (fd 0).
 * Used in headless mode — no soundcard required.
 * Format: 48000 Hz, mono, signed 16-bit little-endian.
 */
class soundStdin : public soundBase
{
public:
  soundStdin();
  ~soundStdin();
  bool init(int samplerate) override;
  void getCardList() override {}

protected:
  int  read(int &countAvailable) override;
  int  write(uint numFrames) override;   // no-op: no audio output in headless
  void flushCapture() override;
  void flushPlayback() override;         // no-op
  void closeDevices() override;
  void waitPlaybackEnd() override;       // no-op

private:
  QFile stdinFile;
};

#endif // SOUNDSTDIN_H

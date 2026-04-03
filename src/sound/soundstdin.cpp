#include "soundstdin.h"
#include "appglobal.h"
#include <QCoreApplication>

soundStdin::soundStdin()
{
}

soundStdin::~soundStdin()
{
  closeDevices();
}

bool soundStdin::init(int samplerate)
{
  sampleRate = samplerate;
  isStereo = false;
  soundDriverOK = stdinFile.open(0, QIODevice::ReadOnly | QIODevice::Unbuffered);
  if (!soundDriverOK) {
    errorHandler("soundStdin", "Failed to open stdin for reading");
  }
  return soundDriverOK;
}

int soundStdin::read(int &countAvailable)
{
  qint64 bytesNeeded = RXSTRIPE * sizeof(qint16);
  qint64 totalRead   = 0;
  char  *buf         = reinterpret_cast<char*>(tempRXBuffer);

  // Loop until we have a full RXSTRIPE-sized block.  A single read() on a pipe
  // returns as soon as *any* bytes are available — which for a UberSDR PCM
  // packet is typically far less than 2048 bytes.  Without this loop the
  // remainder of tempRXBuffer is uninitialised stack garbage, which corrupts
  // the decoder's ring buffer and prevents VIS/sync detection.
  while (totalRead < bytesNeeded) {
    qint64 n = stdinFile.read(buf + totalRead, bytesNeeded - totalRead);
    if (n < 0) {
      // I/O error
      QCoreApplication::quit();
      countAvailable = 0;
      return 0;
    }
    if (n == 0) {
      // EOF
      QCoreApplication::quit();
      countAvailable = 0;
      return 0;
    }
    totalRead += n;
  }

  countAvailable = RXSTRIPE;
  return RXSTRIPE;
}

int soundStdin::write(uint /*numFrames*/)
{
  // No audio output in headless mode
  return 0;
}

void soundStdin::flushCapture()
{
  // Nothing to flush for stdin
}

void soundStdin::flushPlayback()
{
  // No playback
}

void soundStdin::closeDevices()
{
  if (stdinFile.isOpen())
    stdinFile.close();
}

void soundStdin::waitPlaybackEnd()
{
  // No playback
}

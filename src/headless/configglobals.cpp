// configglobals.cpp — headless-only translation unit
// Defines all config globals that are normally defined inside the config/*.cpp
// files (which include ui_*.h forms and cannot compile without a display).
// This file has NO UI dependencies.

#include <QString>
#include <QColor>
#include "soundbase.h"

// ── directoriesconfig.cpp globals ──────────────────────────────────────────
QString rxSSTVImagesPath;
QString rxDRMImagesPath;
QString txSSTVImagesPath;
QString txDRMImagesPath;
QString txStockImagesPath;
QString templatesPath;
QString audioPath;
bool    saveTXimages;
QString docURL;
bool    recursiveScanDirs;

// ── operatorconfig.cpp globals ─────────────────────────────────────────────
QString myCallsign;
QString myQth;
QString myLocator;
QString myLastname;
QString myFirstname;
QString lastReceivedCall;
bool    onlineStatusEnabled;
QString onlineStatusText;

// ── guiconfig.cpp globals ──────────────────────────────────────────────────
int    galleryRows;
int    galleryColumns;
bool   imageStretch;
QColor backGroundColor;
QColor imageBackGroundColor;
bool   slowCPU;
bool   lowRes;
bool   confirmDeletion;
bool   confirmClose;
// defaultImageFormat is defined in sstvrx.cpp (compiled in all builds)

// ── soundconfig.cpp globals ────────────────────────────────────────────────
int    samplingrate;
double rxClock;
double txClock;
bool   pulseSelected;
bool   alsaSelected;
bool   swapChannel;
bool   pttToneOtherChannel;
QString inputAudioDevice;
QString outputAudioDevice;
soundBase::edataSrc soundRoutingInput;
soundBase::edataDst soundRoutingOutput;
quint32 recordingSize;

// ── waterfallconfig.cpp globals ────────────────────────────────────────────
QString startPicWF;
QString endPicWF;
QString fixWF;
QString bsrWF;
QString startBinWF;
QString endBinWF;
QString startRepeaterWF;
QString endRepeaterWF;
QString wfFont;
int     wfFontSize;
bool    wfBold;
QString sampleString;

// ── drmprofileconfig.cpp globals ───────────────────────────────────────────
// (drmprofileconfig defines no plain globals at file scope — only class methods)

// ── cwconfig.cpp globals ───────────────────────────────────────────────────
QString cwText;
int     cwTone;
int     cwWPM;

// ── hybridconfig.cpp globals ───────────────────────────────────────────────
bool    enableHybridRx;
int     hybridFtpPort;
QString hybridFtpRemoteHost;
QString hybridFtpRemoteDirectory;
QString hybridFtpLogin;
QString hybridFtpPassword;
QString hybridFtpHybridFilesDirectory;
bool    enableHybridNotify;
QString hybridNotifyDir;
QString onlineStatusDir;

// ── drmstatusframe.cpp free functions ──────────────────────────────────────
// compactModeToString is defined in drmstatusframe.cpp which pulls in
// ui_drmstatusframe.h. Provide the implementation here for headless builds.
QString compactModeToString(uint mode)
{
  QString tmp;
  switch(mode/10000)
    {
    case 0: tmp+="A"; break;
    case 1: tmp+="B"; break;
    case 2: tmp+="E"; break;
    default: tmp+="-"; break;
    }
  tmp+="/";
  mode-=(mode/10000)*10000;
  switch(mode/1000)
    {
    case 0: tmp+="2.3"; break;
    case 1: tmp+="2.5"; break;
    default:tmp+="---"; break;
    }
  tmp+="/";
  mode-=(mode/1000)*1000;
  switch(mode/100)
    {
    case 0: tmp+="Hi"; break;
    case 1: tmp+="Lo"; break;
    default:tmp+="--" ; break;
    }
  tmp+="/";
  mode-=(mode/100)*100;
  switch(mode/10)
    {
    case 0: tmp+="4"; break;
    case 1: tmp+="16"; break;
    case 2: tmp+="64"; break;
    default: tmp+="--"; break;
    }
  tmp+="/";
  switch(mode&1)
    {
    case 0: tmp+="Long"; break;
    case 1: tmp+="Short"; break;
    default:tmp+="--" ; break;
    }
  return tmp;
}

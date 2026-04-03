/**************************************************************************
*   Copyright (C) 2000-2019 by Johan Maes                                 *
*   on4qz@telenet.be                                                      *
*   https://www.qsl.net/o/on4qz                                           *
*                                                                         *
*   This program is free software; you can redistribute it and/or modify  *
*   it under the terms of the GNU General Public License as published by  *
*   the Free Software Foundation; either version 2 of the License, or     *
*   (at your option) any later version.                                   *
*                                                                         *
*   This program is distributed in the hope that it will be useful,       *
*   but WITHOUT ANY WARRANTY; without even the implied warranty of        *
*   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the         *
*   GNU General Public License for more details.                          *
*                                                                         *
*   You should have received a copy of the GNU General Public License     *
*   along with this program; if not, write to the                         *
*   Free Software Foundation, Inc.,                                       *
*   59 Temple Place - Suite 330, Boston, MA  02111-1307, USA.             *
***************************************************************************/

#include "headlesscontroller.h"
#include "headlessrxcontroller.h"
#include "headlessdispatcher.h"
#include "headlessevents.h"
#include "soundstdin.h"
#include "appglobal.h"
#include "soundbase.h"
#include "sstvparam.h"

#include <QSettings>
#include <QColor>
#include <QDebug>

// Declare config globals directly to avoid pulling in QWidget-based config headers
// (all of which inherit baseConfig -> QWidget and must never be instantiated in
// headless mode).  The definitions live in directoriesconfig.cpp, guiconfig.cpp,
// soundconfig.cpp and sstvparam.cpp — the linker will find them.
extern QString rxSSTVImagesPath;
extern QString rxDRMImagesPath;
extern bool imageStretch;
extern QString defaultImageFormat;
extern QColor imageBackGroundColor;
extern int minCompletion;

// soundconfig.cpp — declared here to avoid including soundconfig.h (pulls in QWidget)
extern soundBase::edataSrc soundRoutingInput;
extern double rxClock;
extern double txClock;

headlessController::headlessController(const QString &outputDir,
                                        int eventsFd,
                                        const QString &freqLabel,
                                        QObject *parent)
  : QObject(parent)
  , outputDir(outputDir)
  , eventsFd(eventsFd)
  , freqLabel(freqLabel)
  , rxCtrl(nullptr)
  , dispatcher(nullptr)
{
}

headlessController::~headlessController()
{
  // rxCtrl and dispatcher are QObject children — deleted automatically
}

void headlessController::loadConfig()
{
  QSettings s;

  // Directories (from directoriesConfig)
  s.beginGroup("DIRECTORIES");
  rxSSTVImagesPath = s.value("rxSSTVImagesPath",
      QString(getenv("HOME")) + "/qsstv/rx_sstv/").toString();
  rxDRMImagesPath  = s.value("rxDRMImagesPath",
      QString(getenv("HOME")) + "/qsstv/rx_drm/").toString();
  s.endGroup();

  // GUI / image settings (from guiConfig)
  s.beginGroup("GUI");
  imageBackGroundColor = s.value("imageBackGroundColor",
      QColor(128, 128, 128)).value<QColor>();
  imageStretch       = s.value("imageStretch",       true).toBool();
  // Default to PNG for lossless headless saves
  defaultImageFormat = s.value("defaultImageFormat", QString("png")).toString();
  // Minimum image completion % before saving (0-100); default 25 matches GUI default
  minCompletion      = s.value("minCompletion",      25).toInt();
  s.endGroup();

  // SSTV settings (from sstvparam globals)
  // autoSave and autoSlantAdjust are always enabled in headless mode —
  // there is no UI to toggle them, and they are the sensible defaults for
  // unattended operation.
  s.beginGroup("SSTV");
  autoSave        = s.value("autoSave",        true).toBool();
  autoSlantAdjust = s.value("autoSlantAdjust", true).toBool();
  // sensitivity: 0=Low, 1=Normal, 2=High, 3=DX — default DX for headless.
  // DX mode suppresses SYNCLOST from missing lines (calcSyncQuality() is
  // gated on sensitivity != NUMBEROFSENSITIVITIES-1), preventing the
  // rewind-and-re-decode that splits one transmission into two images.
  // The frame duration still acts as a natural timeout.
  sensitivity     = s.value("sensitivity",     3).toInt();
  // sstvModeIndexRx: 0 = Auto (syncprocessor checks != 0 for forced mode)
  sstvModeIndexRx = static_cast<esstvMode>(
      s.value("sstvModeIndexRx", 0).toInt());
  s.endGroup();

  // Sound config (from soundConfig) — rxClock/txClock must not be zero
  s.beginGroup("SOUND");
  rxClock = s.value("rxclock", BASESAMPLERATE).toDouble();
  txClock = s.value("txclock", BASESAMPLERATE).toDouble();
  // Reject wildly wrong calibration values (same guard as soundConfig::readSettings)
  if (fabs(1.0 - rxClock / BASESAMPLERATE) > 0.002) rxClock = BASESAMPLERATE;
  if (fabs(1.0 - txClock / BASESAMPLERATE) > 0.002) txClock = BASESAMPLERATE;
  s.endGroup();

  // Sound routing — always stdin in headless mode
  soundRoutingInput = soundBase::SNDFROMSTDIN;

  // Transmission mode — default to SSTV
  s.beginGroup("MAIN");
  transmissionModeIndex = static_cast<etransmissionMode>(
      s.value("transmissionModeIndex", 0).toInt());
  s.endGroup();
}

void headlessController::init()
{
  loadConfig();

  // Initialise structured event output (no-op if eventsFd == -1)
  initHeadlessEvents(eventsFd);

  // Create and initialise the RX controller (owns rxFunctions thread)
  rxCtrl = new headlessRxController(outputDir, freqLabel, this);
  rxCtrl->init();

  // Expose rxCtrl as the global rxWidgetPtr so that modebase.cpp can call
  // rxWidgetPtr->getImageViewerPtr()->getScanLineAddress() during line decode.
  rxWidgetPtr      = rxCtrl;
  headlessCtrlPtr  = this;

  // Create and start the stdin sound source
  soundIOPtr = new soundStdin;
  soundIOPtr->init(BASESAMPLERATE);
  soundIOPtr->start();

  // Create the headless dispatcher and wire it to the RX controller
  dispatcher = new headlessDispatcher(rxCtrl, this);

  // Expose dispatcher as the global dispatcherPtr so that postEvent() calls
  // in rxfunctions.cpp, sstvrx.cpp, modebase.cpp etc. reach it.
  // In headless mode appglobal.h declares dispatcherPtr as QObject*.
  dispatcherPtr = dispatcher;

  dispatcher->init();
}

void headlessController::startRunning()
{
  dispatcher->startRX();
}

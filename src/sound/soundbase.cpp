#include "soundbase.h"
#include "logging.h"
#include "configparams.h"
#include "arraydumper.h"

#include <QDebug>
#include <QApplication>
#include <unistd.h>
#include <time.h>
#include <sys/time.h>

#define FASTCAPTURE

const QString captureStateStr[soundBase::CPEND+1]=
{
  "Capture Init",
  "Capture Starting",
  "Capture Running",
  "Capture Calibrate Starting",
  "Capture Calibrate Wait",
  "Capture Calibrate",
  "Capture End"
};


const QString playbackStateStr[soundBase::PBEND+1]=
{
  "Playback Init",
  "Playback Starting",
  "Playback Running",
  "Playback Calibrate 1",
  "Playback Calibrate 2",
  "Playback End"
};



soundBase::soundBase(QObject *parent) : QThread(parent)
{
  captureState=CPINIT;
  playbackState=PBINIT;
  downsampleFilterPtr=new downsampleFilter(DOWNSAMPLESIZE,true);

}

soundBase::~soundBase()
{
  delete downsampleFilterPtr;
}

void soundBase::run()
{
  stopThread=false;
  unsigned int delay=0;  //todo check use of delay
  while(!stopThread)
    {
      if((captureState==CPINIT) &&   (playbackState==PBINIT))
        {
          msleep(100);
          continue;
        }
      switch (captureState)
        {
        case CPINIT:
          break;
        case CPSTARTING:
          prepareCapture();
          flushCapture();
          rxBuffer.reset(); //clear the rxBuffer
          rxVolumeBuffer.reset();
          switchCaptureState(CPRUNNING);
          break;
        case CPRUNNING:
          if (capture()==0) msleep(1);
          break;
        case CPCALIBRATESTART:
          prepareCapture();
          flushCapture();
          switchCaptureState(CPCALIBRATEWAIT);
          break;
        case CPCALIBRATEWAIT:
          if(captureCalibration(true)==0) msleep(0);
          break;
        case CPCALIBRATE:
          if(captureCalibration(false)==0) msleep(0);
          break;
        case CPEND:
          switchCaptureState(CPINIT);
          break;
        }
      switch(playbackState)
        {
        case PBINIT:
          break;
        case PBSTARTING:
          preparePlayback();
          flushPlayback();
          prebuf=true;
          if (play()==0) msleep(10);
          else
            {
              prebuf=false;
              switchPlaybackState(PBRUNNING);
              addToLog("playback started",LOGSOUND);
            }
          break;
        case PBRUNNING:
          if (play()==0)
            {
              addToLog(QString("playback stopped: delay=%1").arg(delay),LOGSOUND);
              waitPlaybackEnd();
              msleep(delay);
              waveOut.close();
              addToLog("playback stopped",LOGSOUND);
              switchPlaybackState(PBINIT);
            }
          msleep(0);
          break;
        case PBCALIBRATESTART:
          {
            preparePlayback();
            flushPlayback();
            switchPlaybackState(PBCALIBRATEWAIT);
          }
          break;
        case PBCALIBRATEWAIT:
          {
            if(playbackCalibration(true)==0)
              {
                msleep(0);
              }
          }
          break;
        case PBCALIBRATE:
          {
            if(playbackCalibration(false)==0)
              {
                msleep(0);
              }
          }
          break;
        case PBEND:
          switchPlaybackState(PBINIT);
          break;
        }
    }
}

int soundBase::capture()
{
  int count=0;
  if(rxBuffer.spaceLeft()<RXSTRIPE) return 0;
  if(soundRoutingInput==SNDINFROMFILE)
    {
      count=waveIn.read((qint16*)tempRXBuffer,DOWNSAMPLESIZE);
      //delay to give realtime feeling
      if(count<0)
        {
          // we have an error in reading the wav file
          waveIn.close();
          switchCaptureState(CPINIT);
        }
      else if(count==0)
        {
          switchCaptureState(CPEND);
        }
      //    msleep((1000*count)/sampleRate);
      msleep((100*count)/sampleRate);
    }
  else if(soundRoutingInput==SNDFROMSTDIN)
    {
      // Read RXSTRIPE samples at SAMPLERATE (12000 Hz) directly — no decimation.
      // headless QSSTV is UberSDR-only; UberSDR delivers 12000 Hz audio.
      // soundStdin::read() reads RXSTRIPE*sizeof(qint16) bytes and calls
      // QCoreApplication::quit() on EOF.
      count=read(countAvailable);
      if(count==0) return 0;
      // Convert S16LE tempRXBuffer → FILTERPARAMTYPE and write directly to rxBuffer,
      // bypassing downSample4 entirely.
      FILTERPARAMTYPE tmp[RXSTRIPE];
      for(int i=0;i<RXSTRIPE;i++)
        tmp[i]=static_cast<FILTERPARAMTYPE>(tempRXBuffer[i]);
      double sum=0;
      for(int i=0;i<count;i++) sum+=fabs(tmp[i]);
      volume=20.0*log(sum/count+1e-10)-110.0;
      rxBuffer.putNoCheck(tmp,count);
      // rxVolumeBuffer must also be filled — rxFunctions reads RXSTRIPE samples
      // from it unconditionally and passes them to sstvRx::run() as the volume
      // envelope (inputVolumePtr).
      //
      // The sync processor gates on:
      //   inputVol[i] >= minVolume  AND  syncVol[i] > onRatio * inputVol[i]
      //
      // syncVol (detect1200/1900Ptr) is a *smoothed* envelope produced by the
      // IIR bandpass + FIR lowpass chain in syncFilter::process().  inputVol
      // must therefore also be a slowly-varying envelope — NOT raw fabs().
      //
      // Using raw fabs() causes inputVol to oscillate at audio frequency: at
      // zero-crossings inputVol[i] drops below minVolume and the sample is
      // gated out even during a loud SSTV signal, preventing sync detection.
      //
      // Replicate the attack/decay integrator used by the downsample filter
      // (videoFltr: attackIntegrator=0.07, decayIntegrator=0.01) so the sync
      // processor receives the same kind of signal it was designed for.
      FILTERPARAMTYPE vol[RXSTRIPE];
      {
        static FILTERPARAMTYPE stdinEnvLevel = 0;
        const FILTERPARAMTYPE attack = 0.07f;
        const FILTERPARAMTYPE decay  = 0.01f;
        for(int i=0;i<count;i++) {
          FILTERPARAMTYPE absVal = static_cast<FILTERPARAMTYPE>(fabs(tmp[i]));
          if(absVal > stdinEnvLevel)
            stdinEnvLevel += attack * (absVal - stdinEnvLevel);
          else
            stdinEnvLevel += decay  * (absVal - stdinEnvLevel);
          vol[i] = stdinEnvLevel;
        }
      }
      rxVolumeBuffer.putNoCheck(vol,count);

      return count;
    }
  else if(soundDriverOK)
    {
      // read from soundcard
      count=read(countAvailable);
      if(count==0) return 0;
      if(count !=DOWNSAMPLESIZE)
        {
          switchCaptureState(CPINIT);
        }

      if((storedFrames<=(ulong)recordingSize*1048576L) && (soundRoutingInput==SNDINCARDTOFILE))
        {
          addToLog(QString("written %1 tofile").arg(count),LOGSOUND);
          waveOut.write((quint16*)tempRXBuffer,count,false);
          storedFrames+=count;
        }
    }
  downsampleFilterPtr->downSample4(tempRXBuffer);
  volume=downsampleFilterPtr->avgVolumeDb;
  rxBuffer.putNoCheck(downsampleFilterPtr->filteredDataPtr(),RXSTRIPE);
  rxVolumeBuffer.putNoCheck(downsampleFilterPtr->getVolumePtr(),RXSTRIPE);
  return count;
}

int soundBase::captureCalibration(bool leadIn)
{
  int count;
  count=read(countAvailable);

  if(count==0) return 0;
  if(leadIn)
    {
      leadInCounter++;
      if(leadInCounter==CALIBRATIONLEADIN)
        {
          stopwatch.start();
          mutex.lock();
          clock_gettime(CLOCK_MONOTONIC,&ts);
          ustartcalibrationTime=(double)ts.tv_sec +(double)ts.tv_nsec / 1000000000.0;
          calibrationFrames=0;
          mutex.unlock();
          switchCaptureState(CPCALIBRATE);
        }
    }
  else
    {
      mutex.lock();
      calibrationFrames++;
      calibrationTime=stopwatch.elapsed();
      clock_gettime(CLOCK_MONOTONIC,&ts);
      ucalibrationTime=(double)ts.tv_sec +(double)ts.tv_nsec / 1000000000.0 -ustartcalibrationTime;
      mutex.unlock();
      //logFilePtr->addToAux(QString("%1\t%2\t%3").arg(countAvailable).arg(calibrationFrames).arg(calibrationTime) );
    }
  addToLog(QString("read report count:%1 available %2 elapsed qtime %3, time: %4").arg(count).arg(countAvailable).arg(calibrationTime).arg(ucalibrationTime),LOGSOUND);
  return count;
}

bool soundBase::calibrate(bool isCapture)
{
  if (!soundDriverOK) return false;
  switchCaptureState(CPINIT);
  switchPlaybackState(PBINIT);
  calibrationFrames=0;
  calibrationTime=0;
  ucalibrationTime=0;
  leadInCounter=0;
  prevFrames=0;
  if (!isRunning()) start();
  if (isCapture)
    {
      switchCaptureState(CPCALIBRATESTART);
    }
  else
    {
      txBuffer.fill(0);
      switchPlaybackState(PBCALIBRATESTART);
    }
  return true;
}

int soundBase::playbackCalibration(bool leadIn)
{
  int count;
  //  count=write(DOWNSAMPLESIZE);
  count=write(CALIBRATIONSIZE);  // debug joma
  addToLog(QString("calib count %1").arg(count),LOGCALIB);
  if(leadIn)
    {
      leadInCounter++;
      if(leadInCounter==CALIBRATIONLEADIN)
        {
          //      stopwatch.start();
          mutex.lock();
          clock_gettime(CLOCK_MONOTONIC,&ts);
          ustartcalibrationTime=(double)ts.tv_sec +(double)ts.tv_nsec / 1000000000.0;
          addToLog(QString("calib start time %1").arg(ustartcalibrationTime),LOGCALIB);
          calibrationFrames=0;
          mutex.unlock();
          switchPlaybackState(PBCALIBRATE);
        }
    }
  else
    {
      mutex.lock();
      calibrationFrames++;
      clock_gettime(CLOCK_MONOTONIC,&ts);
      ucalibrationTime=(double)ts.tv_sec +(double)ts.tv_nsec / 1000000000.0 -ustartcalibrationTime;
      mutex.unlock();
      //    addToLog(QString("calib time %1 frames %2").arg(ucalibrationTime).arg(calibrationFrames),LOGCALIB);
      //logFilePtr->addToAux(QString("%1\t%2\t%3").arg(countAvailable).arg(calibrationFrames).arg(calibrationTime) );
    }
  return count;
}

bool soundBase::calibrationCount(unsigned int &frames, double &elapsedTime)
{
  mutex.lock();
  frames=calibrationFrames;
  elapsedTime=ucalibrationTime;
  mutex.unlock();
  if(frames==prevFrames) return false;
  prevFrames=frames;
  //  addToLog(QString("calib ok time %1 frames %2").arg(elapsedTime).arg(frames),LOGCALIB);
  return true;
}


void soundBase::idleTX()
{
  waveOut.closeFile();
  waveIn.closeFile();
  playbackState=PBINIT;
}

void soundBase::idleRX()
{
  captureState=CPINIT;


  waveOut.closeFile();
  waveIn.closeFile();

}


void soundBase::stopSoundThread()
{
  idleRX();
  idleTX();
  stopThread=true;
  while(isRunning())
    {
      QApplication::processEvents();
    }
  closeDevices();
}


bool soundBase::startCapture()
{
  switchPlaybackState(PBINIT);
  soundIOPtr->rxBuffer.reset();
//  soundIOPtr->rxVolumeBuffer.reset();
  downsampleFilterPtr->init();
  storedFrames=0;
  switch(soundRoutingInput)
    {
    case SNDINFROMFILE:
      if(!waveIn.openFileForRead("",true))
        {
          errorHandler("File not opened","");
          return false;
        }
      break;
    case SNDINCARDTOFILE:
      {
        if(!soundDriverOK)
          {
            errorHandler("No valid sound device (see configuration)","");
            return false;
          }
        if(!waveOut.openFileForWrite("",true,true)) // always output stereo
          {
            errorHandler("File not opened","");
            return false;
          }
      }
      break;
    case SNDINCARD:
      if(!soundDriverOK)
        {
          errorHandler("No valid sound device (see configuration)","");
          return false;
        }
      break;
    case SNDFROMSTDIN:
      // stdin is already open via soundStdin::init(); nothing to do here
      break;
    }
  switchCaptureState(CPSTARTING);
  return true;
}

int soundBase::play()
{
  unsigned int numFrames;
  int framesWritten;
  if(prebuf)
    {
      if(txBuffer.count()<(DOWNSAMPLESIZE*8))
        {
          return 0;
        }
    }
  if((numFrames=txBuffer.count())>=DOWNSAMPLESIZE) numFrames=DOWNSAMPLESIZE;
  if(numFrames>0)
    {
      framesWritten=0;
    }
  if(soundRoutingOutput==SNDOUTTOFILE)  // output the wav-file
    {

      if(storedFrames<=(ulong)recordingSize*1048576L)
        {
          waveOut.write((quint16*)txBuffer.readPointer(),numFrames,true); //always stereo
          storedFrames+=numFrames;
        }
    }
  txBuffer.copyNoCheck(tempTXBuffer,numFrames);
  addToLog(QString("frames to write: %1 at %2 buffered:%3").arg(numFrames).arg(txBuffer.getReadIndex()).arg(txBuffer.count()),LOGSOUND);

  //  framesWritten=write(numFrames);
  framesWritten=write(DOWNSAMPLESIZE);
  addToLog(QString("frames written: %1").arg(framesWritten),LOGSOUND);
  if(framesWritten<0)
    {
      addToLog("Sound write error",LOGSOUND);
    }
  return numFrames;
}

bool soundBase::startPlayback()
{
  switchCaptureState(CPINIT);
  if(!soundDriverOK)
    {
      errorHandler("No valid sound device (see configuration)","");
      return false;
    }
  storedFrames=0;
  soundIOPtr->txBuffer.reset();
  if(soundRoutingOutput==SNDOUTTOFILE)
    {

      if(!waveOut.openFileForWrite("",true,true)) // indicate stereo
        {
          errorHandler("File not opened","");
          return false;
        }
    }
  playbackState=PBSTARTING;

  addToLog(QString("start playback, txbuffercount: %1").arg(txBuffer.count()),LOGSOUND);
  return true;
}


void soundBase::errorHandler(QString title, QString info)
{
  addToLog(title+" "+info,LOGSOUND);
  lastErrorStr=title+" "+info;
}

void soundBase::switchCaptureState(ecaptureState cs)
{
  addToLog(QString("Switching from captureState %1 to %2").arg(captureStateStr[captureState]).arg(captureStateStr[cs]),LOGSOUND);
  captureState=cs;
}

void soundBase::switchPlaybackState(eplaybackState ps)
{
  addToLog(QString("Switching from playbackState %1 to %2").arg(playbackStateStr[playbackState]).arg(playbackStateStr[ps]),LOGSOUND);
  playbackState=ps;
}

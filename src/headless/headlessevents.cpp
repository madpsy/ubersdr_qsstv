#include "headlessevents.h"

#include <QFile>
#include <QJsonDocument>
#include <QMutex>
#include <QMutexLocker>

static int    g_eventsFd = -1;
static QFile  g_eventsFile;
static QMutex g_mutex;

void initHeadlessEvents(int fd)
{
  QMutexLocker lock(&g_mutex);
  if (fd < 0) return;
  g_eventsFd = fd;
  g_eventsFile.open(fd, QIODevice::WriteOnly | QIODevice::Unbuffered,
                    QFileDevice::DontCloseHandle);
}

void writeHeadlessEvent(const QJsonObject &obj)
{
  QMutexLocker lock(&g_mutex);
  if (!g_eventsFile.isOpen()) return;
  QByteArray line = QJsonDocument(obj).toJson(QJsonDocument::Compact);
  line.append('\n');
  g_eventsFile.write(line);
}

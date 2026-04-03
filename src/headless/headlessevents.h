#ifndef HEADLESSEVENTS_H
#define HEADLESSEVENTS_H

#include <QJsonObject>

/**
 * @brief Structured JSON event output for headless mode.
 *
 * The Go parent process (ubersdr_qsstv) reads newline-delimited JSON from
 * the file descriptor passed via --events-fd.  Each call to
 * writeHeadlessEvent() writes one compact JSON object followed by '\n'.
 *
 * If initHeadlessEvents() is never called (fd == -1) all writes are no-ops,
 * so the headless binary remains usable standalone without a parent process.
 */

/**
 * @brief Open the events file descriptor.
 * @param fd  File descriptor number passed via --events-fd, or -1 to disable.
 */
void initHeadlessEvents(int fd);

/**
 * @brief Write one JSON event line to the events fd.
 *
 * Thread-safe: may be called from any Qt thread.
 * No-op if initHeadlessEvents() was not called or fd == -1.
 */
void writeHeadlessEvent(const QJsonObject &obj);

#endif // HEADLESSEVENTS_H

#ifndef HEADLESSIMAGEBUFFER_H
#define HEADLESSIMAGEBUFFER_H

#include <QImage>
#include <QSize>
#include <QColor>
#include <QRgb>

/**
 * @brief Lightweight image buffer for headless mode.
 *
 * Provides the same two methods that modebase.cpp calls via
 * rxWidgetPtr->getImageViewerPtr():
 *   - createImage(QSize, QColor, bool)
 *   - getScanLineAddress(int)
 *
 * Unlike imageViewer (which is a QLabel/QWidget), this class has no GUI
 * dependency and works correctly with QCoreApplication.
 */
class headlessImageBuffer
{
public:
  headlessImageBuffer() {}

  /**
   * @brief Allocate a new image filled with the given colour.
   * @param sz    Image dimensions in pixels.
   * @param fill  Background fill colour.
   * @param scale Ignored in headless mode (no display to scale for).
   */
  void createImage(QSize sz, QColor fill, bool /*scale*/)
  {
    image = QImage(sz, QImage::Format_ARGB32_Premultiplied);
    if (!image.isNull())
      image.fill(fill);
  }

  /**
   * @brief Return a pointer to the start of scan line @p line.
   * @return Pointer to the first QRgb pixel of the line, or nullptr if
   *         the image is null or @p line is out of range.
   */
  QRgb *getScanLineAddress(int line)
  {
    if (image.isNull() || line < 0 || line >= image.height())
      return nullptr;
    return reinterpret_cast<QRgb *>(image.scanLine(line));
  }

  /** @brief Access the underlying QImage (e.g. to save it). */
  QImage &getImage() { return image; }
  const QImage &getImage() const { return image; }

private:
  QImage image;
};

#endif // HEADLESSIMAGEBUFFER_H

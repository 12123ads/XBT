package com.github.enderwolf006.xbt

import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.view.View
import kotlin.math.max
import kotlin.math.min

/**
 * Barcode bounds in the coordinate space ML Kit reports them in: the camera buffer with
 * `ImageProxy.imageInfo.rotationDegrees` applied, origin at the top-left of the upright image.
 */
internal data class BarcodeBox(
    val left: Float,
    val top: Float,
    val right: Float,
    val bottom: Float,
) {
    val width: Float get() = right - left
    val height: Float get() = bottom - top
    val centerX: Float get() = (left + right) / 2f
    val centerY: Float get() = (top + bottom) / 2f
}

internal object BarcodeOverlayGeometry {
    /**
     * Maps an ML Kit bounding box to `PreviewView` coordinates.
     *
     * ML Kit rotates the analysis buffer by `rotationDegrees` before detecting, so the box is in
     * upright image space. `PreviewView` shows that same image centred:
     * - FILL_CENTER scales by the larger ratio and crops the overflow;
     * - FIT_CENTER scales by the smaller ratio and letterboxes;
     * - the front camera preview is mirrored horizontally.
     *
     * Both `Preview` and `ImageAnalysis` are pinned to the same 4:3 aspect ratio, so their
     * visible field of view is identical and no per-use-case crop correction is needed.
     */
    fun mapToPreview(
        box: BarcodeBox,
        imageWidth: Int,
        imageHeight: Int,
        rotationDegrees: Int,
        mirrored: Boolean,
        fillCenter: Boolean,
        viewWidth: Float,
        viewHeight: Float,
    ): BarcodeBox {
        require(viewWidth > 0f && viewHeight > 0f) { "view size must be positive" }
        val swapDimensions = rotationDegrees % 180 != 0
        val uprightWidth = (if (swapDimensions) imageHeight else imageWidth).toFloat()
        val uprightHeight = (if (swapDimensions) imageWidth else imageHeight).toFloat()
        require(uprightWidth > 0f && uprightHeight > 0f) { "image size must be positive" }

        val scale = if (fillCenter) {
            max(viewWidth / uprightWidth, viewHeight / uprightHeight)
        } else {
            min(viewWidth / uprightWidth, viewHeight / uprightHeight)
        }
        val offsetX = (viewWidth - uprightWidth * scale) / 2f
        val offsetY = (viewHeight - uprightHeight * scale) / 2f

        val left = offsetX + box.left * scale
        val right = offsetX + box.right * scale
        val top = offsetY + box.top * scale
        val bottom = offsetY + box.bottom * scale

        return if (mirrored) {
            BarcodeBox(viewWidth - right, top, viewWidth - left, bottom)
        } else {
            BarcodeBox(left, top, right, bottom)
        }
    }
}

/**
 * Draws exactly one barcode frame. It is a plain non-interactive view: touches and
 * accessibility focus must keep flowing to the WebView above it.
 */
internal class BarcodeOverlayView(context: Context) : View(context) {
    private val density = resources.displayMetrics.density
    private val framePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeWidth = 2f * density
        strokeCap = Paint.Cap.ROUND
        color = 0xB322D3EE.toInt()
    }
    private val cornerPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeWidth = 3f * density
        strokeCap = Paint.Cap.ROUND
        color = 0xFF22D3EE.toInt()
    }
    private val cornerRadius = 8f * density
    private var box: BarcodeBox? = null

    init {
        isClickable = false
        isFocusable = false
        importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_NO
        setWillNotDraw(false)
    }

    fun show(box: BarcodeBox) {
        if (this.box == box) return
        this.box = box
        invalidate()
    }

    fun clear() {
        if (box == null) return
        box = null
        invalidate()
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val box = box ?: return
        if (box.width <= 0f || box.height <= 0f) return

        canvas.drawRoundRect(
            box.left,
            box.top,
            box.right,
            box.bottom,
            cornerRadius,
            cornerRadius,
            framePaint,
        )

        val shortSide = min(box.width, box.height)
        val cornerLength = (shortSide * 0.28f)
            .coerceIn(14f * density, 48f * density)
            .coerceAtMost(shortSide / 2f)
        if (cornerLength <= 0f) return

        drawCorner(canvas, box.left, box.top, cornerLength, 1f, 1f)
        drawCorner(canvas, box.right, box.top, cornerLength, -1f, 1f)
        drawCorner(canvas, box.left, box.bottom, cornerLength, 1f, -1f)
        drawCorner(canvas, box.right, box.bottom, cornerLength, -1f, -1f)
    }

    private fun drawCorner(
        canvas: Canvas,
        x: Float,
        y: Float,
        length: Float,
        dx: Float,
        dy: Float,
    ) {
        canvas.drawLine(x, y, x + dx * length, y, cornerPaint)
        canvas.drawLine(x, y, x, y + dy * length, cornerPaint)
    }
}

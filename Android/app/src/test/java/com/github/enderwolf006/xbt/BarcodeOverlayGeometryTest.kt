package com.github.enderwolf006.xbt

import org.junit.Assert.assertEquals
import org.junit.Test

class BarcodeOverlayGeometryTest {

    private fun assertBox(
        expectedLeft: Float,
        expectedTop: Float,
        expectedRight: Float,
        expectedBottom: Float,
        actual: BarcodeBox,
    ) {
        val delta = 0.001f
        assertEquals("left", expectedLeft, actual.left, delta)
        assertEquals("top", expectedTop, actual.top, delta)
        assertEquals("right", expectedRight, actual.right, delta)
        assertEquals("bottom", expectedBottom, actual.bottom, delta)
    }

    @Test
    fun rotation90UsesSwappedUprightDimensions() {
        // 640x480 buffer rotated 90 degrees is a 480x640 upright image. FILL_CENTER on a
        // 1080x2400 view scales by 3.75 and crops the width.
        val mapped = BarcodeOverlayGeometry.mapToPreview(
            box = BarcodeBox(190f, 270f, 290f, 370f),
            imageWidth = 640,
            imageHeight = 480,
            rotationDegrees = 90,
            mirrored = false,
            fillCenter = true,
            viewWidth = 1080f,
            viewHeight = 2400f,
        )

        assertBox(
            expectedLeft = 352.5f,
            expectedTop = 1012.5f,
            expectedRight = 727.5f,
            expectedBottom = 1387.5f,
            actual = mapped,
        )
    }

    @Test
    fun rotation90KeepsImageCenterAtViewCenter() {
        val mapped = BarcodeOverlayGeometry.mapToPreview(
            box = BarcodeBox(240f, 320f, 240f, 320f),
            imageWidth = 640,
            imageHeight = 480,
            rotationDegrees = 90,
            mirrored = false,
            fillCenter = true,
            viewWidth = 1080f,
            viewHeight = 2400f,
        )

        assertEquals(540f, mapped.centerX, 0.001f)
        assertEquals(1200f, mapped.centerY, 0.001f)
    }

    @Test
    fun rotation0KeepsImageCenterAtViewCenter() {
        val mapped = BarcodeOverlayGeometry.mapToPreview(
            box = BarcodeBox(320f, 240f, 320f, 240f),
            imageWidth = 640,
            imageHeight = 480,
            rotationDegrees = 0,
            mirrored = false,
            fillCenter = true,
            viewWidth = 1080f,
            viewHeight = 2400f,
        )

        assertEquals(540f, mapped.centerX, 0.001f)
        assertEquals(1200f, mapped.centerY, 0.001f)
    }

    @Test
    fun fitCenterLetterboxesInsteadOfCropping() {
        val mapped = BarcodeOverlayGeometry.mapToPreview(
            box = BarcodeBox(0f, 0f, 480f, 640f),
            imageWidth = 640,
            imageHeight = 480,
            rotationDegrees = 90,
            mirrored = false,
            fillCenter = false,
            viewWidth = 1080f,
            viewHeight = 2400f,
        )

        assertBox(0f, 480f, 1080f, 1920f, mapped)
    }

    @Test
    fun frontCameraMirrorsHorizontally() {
        val mapped = BarcodeOverlayGeometry.mapToPreview(
            box = BarcodeBox(10f, 20f, 110f, 120f),
            imageWidth = 480,
            imageHeight = 640,
            rotationDegrees = 0,
            mirrored = true,
            fillCenter = true,
            viewWidth = 480f,
            viewHeight = 640f,
        )

        assertBox(370f, 20f, 470f, 120f, mapped)
    }

    @Test
    fun rotation180DoesNotSwapDimensions() {
        val mapped = BarcodeOverlayGeometry.mapToPreview(
            box = BarcodeBox(10f, 20f, 110f, 120f),
            imageWidth = 640,
            imageHeight = 480,
            rotationDegrees = 180,
            mirrored = false,
            fillCenter = true,
            viewWidth = 640f,
            viewHeight = 480f,
        )

        assertBox(10f, 20f, 110f, 120f, mapped)
    }

    @Test
    fun offsetBoxCenterScalesAroundImageCenter() {
        val mapped = BarcodeOverlayGeometry.mapToPreview(
            box = BarcodeBox(100f, 100f, 200f, 200f),
            imageWidth = 640,
            imageHeight = 480,
            rotationDegrees = 90,
            mirrored = false,
            fillCenter = true,
            viewWidth = 1000f,
            viewHeight = 2000f,
        )

        assertBox(62.5f, 312.5f, 375f, 625f, mapped)
    }
}

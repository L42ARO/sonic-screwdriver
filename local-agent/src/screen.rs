//! Screen capture, downscaling to a model-friendly size, zoom crops, and settle detection.

use std::io::Cursor;
use std::thread;
use std::time::{Duration, Instant};

use anyhow::{anyhow, Context, Result};
use image::{imageops::FilterType, DynamicImage, ImageFormat, RgbaImage};
use xcap::Monitor;

use crate::protocol::Shot;

pub struct Screen {
    monitor: Monitor,
    /// Monitor origin in physical pixels (primary is normally 0,0).
    origin: (i32, i32),
    pub phys_w: u32,
    pub phys_h: u32,
    pub shot_w: u32,
    pub shot_h: u32,
    /// Physical pixels per screenshot pixel, >= 1.0.
    pub scale: f32,
}

impl Screen {
    /// Use the primary monitor. Screenshots are downscaled so their width is at most `max_width`.
    pub fn new(max_width: u32) -> Result<Self> {
        let monitors = Monitor::all().context("listing monitors")?;
        let monitor = monitors
            .into_iter()
            .find(|m| m.is_primary().unwrap_or(false))
            .ok_or_else(|| anyhow!("no primary monitor"))?;
        let phys_w = monitor.width()?;
        let phys_h = monitor.height()?;
        let origin = (monitor.x()?, monitor.y()?);
        let scale = if phys_w > max_width { phys_w as f32 / max_width as f32 } else { 1.0 };
        let shot_w = (phys_w as f32 / scale).round() as u32;
        let shot_h = (phys_h as f32 / scale).round() as u32;
        Ok(Self { monitor, origin, phys_w, phys_h, shot_w, shot_h, scale })
    }

    /// Screenshot-space point -> absolute physical screen point.
    pub fn to_physical(&self, x: i32, y: i32) -> (i32, i32) {
        (
            self.origin.0 + (x as f32 * self.scale).round() as i32,
            self.origin.1 + (y as f32 * self.scale).round() as i32,
        )
    }

    /// Physical rect -> screenshot-space rect.
    pub fn to_shot_rect(&self, x: i32, y: i32, w: i32, h: i32) -> [i32; 4] {
        let f = |v: i32| (v as f32 / self.scale).round() as i32;
        [f(x - self.origin.0), f(y - self.origin.1), f(w), f(h)]
    }

    pub fn capture_raw(&self) -> Result<RgbaImage> {
        self.monitor.capture_image().context("capturing monitor")
    }

    /// Full screenshot, downscaled to fit `max_width`, PNG-encoded.
    pub fn screenshot(&self) -> Result<Shot> {
        let raw = self.capture_raw()?;
        let img = if self.scale > 1.0 {
            image::imageops::resize(&raw, self.shot_w, self.shot_h, FilterType::Triangle)
        } else {
            raw
        };
        Ok(Shot {
            width: img.width(),
            height: img.height(),
            scale: self.scale,
            region: None,
            png: encode_png(img)?,
            path: None,
            png_base64: None,
        })
    }

    /// Crop a screenshot-space rect at full physical resolution. If the crop is wider than
    /// the normal screenshot it is downscaled to that width so it still fits model limits.
    pub fn zoom(&self, x: u32, y: u32, w: u32, h: u32) -> Result<Shot> {
        if w == 0 || h == 0 {
            return Err(anyhow!("zoom region must have non-zero size"));
        }
        let s = self.scale;
        let px = ((x as f32) * s).round() as u32;
        let py = ((y as f32) * s).round() as u32;
        let pw = ((w as f32) * s).round().max(1.0) as u32;
        let ph = ((h as f32) * s).round().max(1.0) as u32;
        if px + pw > self.phys_w || py + ph > self.phys_h {
            return Err(anyhow!(
                "zoom region {x},{y} {w}x{h} exceeds the {}x{} screenshot",
                self.shot_w, self.shot_h
            ));
        }
        let mut img = self.monitor.capture_region(px, py, pw, ph).context("capturing region")?;
        let mut scale = 1.0 / s; // physical px per zoom px: zoom shows more detail than the screenshot
        if img.width() > self.shot_w {
            let f = self.shot_w as f32 / img.width() as f32;
            let nh = ((img.height() as f32) * f).round().max(1.0) as u32;
            img = image::imageops::resize(&img, self.shot_w, nh, FilterType::Triangle);
            scale /= f;
        }
        Ok(Shot {
            width: img.width(),
            height: img.height(),
            scale,
            region: Some([x, y, w, h]),
            png: encode_png(img)?,
            path: None,
            png_base64: None,
        })
    }

    /// Poll the screen until two consecutive captures are (almost) identical, or `timeout`
    /// elapses. Returns true if it settled, false on timeout. Tolerates a blinking caret.
    pub fn wait_settled(&self, timeout: Duration) -> Result<bool> {
        let start = Instant::now();
        let mut prev = self.capture_raw()?;
        loop {
            thread::sleep(Duration::from_millis(120));
            let cur = self.capture_raw()?;
            if nearly_same(&prev, &cur) {
                return Ok(true);
            }
            if start.elapsed() > timeout {
                return Ok(false);
            }
            prev = cur;
        }
    }
}

/// True when fewer than 0.02% of pixels differ (enough to ignore a caret or clock tick).
fn nearly_same(a: &RgbaImage, b: &RgbaImage) -> bool {
    if a.dimensions() != b.dimensions() {
        return false;
    }
    let total = (a.width() * a.height()) as usize;
    let budget = total / 5000 + 1;
    let mut diff = 0usize;
    for (pa, pb) in a.pixels().zip(b.pixels()) {
        if pa.0[..3] != pb.0[..3] {
            diff += 1;
            if diff > budget {
                return false;
            }
        }
    }
    true
}

fn encode_png(img: RgbaImage) -> Result<Vec<u8>> {
    let rgb = DynamicImage::ImageRgba8(img).to_rgb8();
    let mut buf = Vec::new();
    DynamicImage::ImageRgb8(rgb)
        .write_to(&mut Cursor::new(&mut buf), ImageFormat::Png)
        .context("encoding png")?;
    Ok(buf)
}

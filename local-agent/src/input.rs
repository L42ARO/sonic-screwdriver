//! Mouse and keyboard injection. All coordinates here are absolute physical pixels.

use std::thread;
use std::time::Duration;

use anyhow::{anyhow, Result};
use enigo::{Axis, Button, Coordinate, Direction, Enigo, Key, Keyboard, Mouse, Settings};

use crate::protocol::MouseButton;

pub struct Input {
    enigo: Enigo,
}

const MOVE_SETTLE: Duration = Duration::from_millis(25);
const CLICK_GAP: Duration = Duration::from_millis(60);

impl Input {
    pub fn new() -> Result<Self> {
        let enigo = Enigo::new(&Settings::default()).map_err(|e| anyhow!("input init: {e}"))?;
        Ok(Self { enigo })
    }

    pub fn move_to(&mut self, x: i32, y: i32) -> Result<()> {
        self.enigo.move_mouse(x, y, Coordinate::Abs).map_err(|e| anyhow!("move: {e}"))?;
        thread::sleep(MOVE_SETTLE);
        Ok(())
    }

    pub fn click(&mut self, x: i32, y: i32, button: MouseButton, count: u8) -> Result<()> {
        self.move_to(x, y)?;
        let b = match button {
            MouseButton::Left => Button::Left,
            MouseButton::Right => Button::Right,
            MouseButton::Middle => Button::Middle,
        };
        for i in 0..count.max(1) {
            if i > 0 {
                thread::sleep(CLICK_GAP);
            }
            self.enigo.button(b, Direction::Click).map_err(|e| anyhow!("click: {e}"))?;
        }
        Ok(())
    }

    pub fn drag(&mut self, from: (i32, i32), to: (i32, i32)) -> Result<()> {
        self.move_to(from.0, from.1)?;
        self.enigo.button(Button::Left, Direction::Press).map_err(|e| anyhow!("drag press: {e}"))?;
        let steps = 12;
        for i in 1..=steps {
            let t = i as f32 / steps as f32;
            let x = from.0 + ((to.0 - from.0) as f32 * t).round() as i32;
            let y = from.1 + ((to.1 - from.1) as f32 * t).round() as i32;
            self.enigo.move_mouse(x, y, Coordinate::Abs).map_err(|e| anyhow!("drag move: {e}"))?;
            thread::sleep(Duration::from_millis(15));
        }
        self.enigo.button(Button::Left, Direction::Release).map_err(|e| anyhow!("drag release: {e}"))?;
        Ok(())
    }

    /// dy > 0 scrolls down, dx > 0 scrolls right (wheel notches).
    pub fn scroll(&mut self, x: i32, y: i32, dx: i32, dy: i32) -> Result<()> {
        self.move_to(x, y)?;
        if dy != 0 {
            self.enigo.scroll(dy, Axis::Vertical).map_err(|e| anyhow!("scroll: {e}"))?;
        }
        if dx != 0 {
            self.enigo.scroll(dx, Axis::Horizontal).map_err(|e| anyhow!("scroll: {e}"))?;
        }
        Ok(())
    }

    pub fn type_text(&mut self, text: &str) -> Result<()> {
        self.enigo.text(text).map_err(|e| anyhow!("type: {e}"))
    }

    /// Press a chord like "ctrl+shift+s", "alt+f4", "enter", "f3", or a single character,
    /// `count` times.
    pub fn key_chord_n(&mut self, chord: &str, count: u8) -> Result<()> {
        for i in 0..count.max(1) {
            if i > 0 {
                thread::sleep(Duration::from_millis(40));
            }
            self.key_chord(chord)?;
        }
        Ok(())
    }

    /// Press a chord like "ctrl+shift+s", "alt+f4", "enter", "f3", or a single character.
    pub fn key_chord(&mut self, chord: &str) -> Result<()> {
        let parts: Vec<&str> = chord.split('+').map(str::trim).filter(|s| !s.is_empty()).collect();
        if parts.is_empty() {
            return Err(anyhow!("empty key chord"));
        }
        let (main, mods) = parts.split_last().unwrap();
        let mods: Vec<Key> = mods.iter().map(|m| parse_key(m)).collect::<Result<_>>()?;
        let main = parse_key(main)?;
        for m in &mods {
            self.enigo.key(*m, Direction::Press).map_err(|e| anyhow!("key press: {e}"))?;
        }
        let r = self.enigo.key(main, Direction::Click).map_err(|e| anyhow!("key: {e}"));
        for m in mods.iter().rev() {
            self.enigo.key(*m, Direction::Release).map_err(|e| anyhow!("key release: {e}"))?;
        }
        r
    }
}

fn parse_key(name: &str) -> Result<Key> {
    let n = name.to_ascii_lowercase();
    Ok(match n.as_str() {
        "ctrl" | "control" => Key::Control,
        "alt" => Key::Alt,
        "shift" => Key::Shift,
        "win" | "meta" | "super" | "cmd" => Key::Meta,
        "enter" | "return" => Key::Return,
        "tab" => Key::Tab,
        "esc" | "escape" => Key::Escape,
        "backspace" => Key::Backspace,
        "delete" | "del" => Key::Delete,
        "space" => Key::Space,
        "up" => Key::UpArrow,
        "down" => Key::DownArrow,
        "left" => Key::LeftArrow,
        "right" => Key::RightArrow,
        "home" => Key::Home,
        "end" => Key::End,
        "pageup" | "pgup" => Key::PageUp,
        "pagedown" | "pgdn" => Key::PageDown,
        "f1" => Key::F1,
        "f2" => Key::F2,
        "f3" => Key::F3,
        "f4" => Key::F4,
        "f5" => Key::F5,
        "f6" => Key::F6,
        "f7" => Key::F7,
        "f8" => Key::F8,
        "f9" => Key::F9,
        "f10" => Key::F10,
        "f11" => Key::F11,
        "f12" => Key::F12,
        _ => {
            let mut chars = name.chars();
            match (chars.next(), chars.next()) {
                (Some(c), None) => Key::Unicode(c),
                _ => return Err(anyhow!("unknown key name: {name:?}")),
            }
        }
    })
}

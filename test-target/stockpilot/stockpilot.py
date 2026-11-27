#!/usr/bin/env python3
"""
StockPilot Inventory Manager 2014 - a fake early-2010s inventory program.

A test target for the computer-use agent, a decade newer than DentaSoft: flat "Metro"
styling, sidebar navigation, a searchable, sortable, paged product grid, dropdowns,
checkboxes, stacked dialogs, toasts that disappear on their own, and a "Tip of the day"
popup at startup. Text boxes behave like modern ones (Ctrl+A, Home, End, Delete, arrows).

Like DentaSoft, every control is painted on ONE Tk Canvas: there is no accessibility
tree, so the only interface is pixels in, mouse and keyboard out.

Usage:
    python stockpilot.py            # run (creates stockpilot.json from the seed on first run)
    python stockpilot.py --reset    # restore the seed data first
    python stockpilot.py --fast     # no artificial delays (development only)
"""
import json
import math
import os
import random
import shutil
import sys
import time
import tkinter as tk
from tkinter import font as tkfont

if getattr(sys, "frozen", False):
    # Packaged as an .exe (release zip): the seed is bundled inside the exe, and the live
    # data is kept next to the exe so edits survive restarts.
    HERE = os.path.dirname(sys.executable)
    SEED = os.path.join(getattr(sys, "_MEIPASS", HERE), "stockpilot.seed.json")
else:
    HERE = os.path.dirname(os.path.abspath(__file__))
    SEED = os.path.join(HERE, "stockpilot.seed.json")
DATA = os.path.join(HERE, "stockpilot.json")

W, H = 1100, 680
BG, WHITE, TEXT, MUTED = "#f3f3f3", "#ffffff", "#252525", "#6d6d6d"
LINE, BORDER, ACCENT, ACCENT_DARK = "#e1e1e1", "#c8c8c8", "#2b579a", "#1e4e8c"
SIDE, SIDE_SEL, SEL, SELTEXT = "#2b579a", "#1f4680", "#cde6f7", "#b3d7ff"
DANGER, OK, WARN, TOAST = "#c42b1c", "#107c10", "#f7e7a1", "#2d2d30"
CATEGORIES = ["Electrical", "Fasteners", "Paint", "Plumbing", "Safety", "Tools"]
REASONS = ["Received", "Sold", "Damaged", "Count correction"]
PAGE = 15
DELAY = (0.2, 0.7)
TIPS = [
    "You can sort the product list by clicking any column header.",
    "Double-click a product to see its details and adjust stock.",
    "Tick 'Low stock only' to see products at or below their reorder level.",
]


# ============================================================================ widgets
class Widget:
    focusable = False

    def __init__(self, x, y, w, h):
        self.x, self.y, self.w, self.h = x, y, w, h
        self.enabled = True

    def contains(self, px, py):
        return self.x <= px < self.x + self.w and self.y <= py < self.y + self.h

    def click(self, app, px, py):
        pass

    def key(self, app, e):
        return False


class Button(Widget):
    def __init__(self, label, x, y, w, h, cmd, primary=False):
        super().__init__(x, y, w, h)
        self.label, self.cmd, self.primary = label, cmd, primary

    def draw(self, c, app):
        if self.primary and self.enabled:
            hover = app.hover is self
            c.create_rectangle(self.x, self.y, self.x + self.w, self.y + self.h,
                               fill=ACCENT_DARK if hover else ACCENT, outline="")
            fg = WHITE
        else:
            c.create_rectangle(self.x, self.y, self.x + self.w, self.y + self.h,
                               fill="#e9e9e9" if app.hover is self and self.enabled else WHITE, outline=BORDER)
            fg = TEXT if self.enabled else "#b4b4b4"
        c.create_text(self.x + self.w // 2, self.y + self.h // 2, text=self.label, font=app.f_ui, fill=fg)

    def click(self, app, px, py):
        if self.enabled:
            self.cmd()


class TextBox(Widget):
    focusable = True

    def __init__(self, x, y, w, value="", placeholder="", on_change=None, h=28, maxlen=60):
        super().__init__(x, y, w, h)
        self.value, self.placeholder, self.on_change, self.maxlen = value, placeholder, on_change, maxlen
        self.caret, self.all_selected = len(value), False

    def set(self, value):
        self.value, self.caret, self.all_selected = value, len(value), False

    def draw(self, c, app):
        focused = app.focus is self
        c.create_rectangle(self.x, self.y, self.x + self.w, self.y + self.h, fill=WHITE,
                           outline=ACCENT if focused else BORDER, width=2 if focused else 1)
        f = app.f_ui
        tx, ty = self.x + 7, self.y + self.h // 2
        if not self.value and not focused:
            c.create_text(tx, ty, text=self.placeholder, anchor="w", font=f, fill="#9a9a9a")
            return
        shown, offset = self.value, 0
        while f.measure(shown) > self.w - 16 and shown:
            shown, offset = shown[1:], offset + 1
        if self.all_selected and self.value:
            c.create_rectangle(tx - 1, self.y + 5, tx + f.measure(shown) + 1, self.y + self.h - 5, fill=SELTEXT, outline="")
        c.create_text(tx, ty, text=shown, anchor="w", font=f, fill=TEXT)
        if focused and app.caret_on and not self.all_selected:
            cx = tx + f.measure(shown[:max(0, self.caret - offset)])
            c.create_line(cx, self.y + 6, cx, self.y + self.h - 6, fill=TEXT)

    def click(self, app, px, py):
        app.set_focus(self)
        f, x = app.f_ui, self.x + 7
        best, best_d = len(self.value), 10 ** 9
        for i in range(len(self.value) + 1):
            d = abs(x + f.measure(self.value[:i]) - px)
            if d < best_d:
                best, best_d = i, d
        self.caret, self.all_selected = best, False

    def key(self, app, e):
        ks, ctrl = e.keysym, bool(e.state & 0x4)
        before = self.value
        if ctrl and ks.lower() == "a":
            self.all_selected = True
            return True
        if ctrl:
            return False
        if self.all_selected and (ks in ("BackSpace", "Delete") or (e.char and e.char.isprintable())):
            self.value, self.caret, self.all_selected = "", 0, False
            if ks in ("BackSpace", "Delete"):
                self._changed(before)
                return True
        self.all_selected = False
        if ks == "BackSpace":
            if self.caret > 0:
                self.value = self.value[:self.caret - 1] + self.value[self.caret:]
                self.caret -= 1
        elif ks == "Delete":
            self.value = self.value[:self.caret] + self.value[self.caret + 1:]
        elif ks == "Left":
            self.caret = max(0, self.caret - 1)
        elif ks == "Right":
            self.caret = min(len(self.value), self.caret + 1)
        elif ks == "Home":
            self.caret = 0
        elif ks == "End":
            self.caret = len(self.value)
        elif e.char and e.char.isprintable() and len(self.value) < self.maxlen:
            self.value = self.value[:self.caret] + e.char + self.value[self.caret:]
            self.caret += 1
        else:
            return False
        self._changed(before)
        return True

    def _changed(self, before):
        if self.value != before and self.on_change:
            self.on_change()


class Combo(Widget):
    ITEM = 26

    def __init__(self, x, y, w, options, value=None, placeholder="Select...", on_change=None, h=28):
        super().__init__(x, y, w, h)
        self.options, self.value, self.placeholder, self.on_change = options, value, placeholder, on_change

    def draw(self, c, app):
        open_ = app.popup is self
        c.create_rectangle(self.x, self.y, self.x + self.w, self.y + self.h, fill=WHITE,
                           outline=ACCENT if open_ else BORDER)
        c.create_text(self.x + 8, self.y + self.h // 2, text=self.value or self.placeholder, anchor="w",
                      font=app.f_ui, fill=TEXT if self.value else "#9a9a9a")
        ax, ay = self.x + self.w - 16, self.y + self.h // 2
        c.create_polygon(ax - 5, ay - 2, ax + 5, ay - 2, ax, ay + 4, fill=MUTED, outline="")

    def draw_popup(self, c, app):
        top = self.y + self.h
        c.create_rectangle(self.x, top, self.x + self.w, top + len(self.options) * self.ITEM + 2, fill=WHITE, outline=BORDER)
        for i, opt in enumerate(self.options):
            iy = top + 1 + i * self.ITEM
            if opt == self.value:
                c.create_rectangle(self.x + 1, iy, self.x + self.w - 1, iy + self.ITEM, fill=SEL, outline="")
            c.create_text(self.x + 8, iy + self.ITEM // 2, text=opt, anchor="w", font=app.f_ui, fill=TEXT)

    def popup_click(self, app, px, py):
        top = self.y + self.h
        i = (py - top - 1) // self.ITEM
        if self.x <= px < self.x + self.w and 0 <= i < len(self.options) and py > top:
            self.value = self.options[i]
            app.popup = None
            if self.on_change:
                app.with_delay(self.on_change)
            return True
        return False

    def click(self, app, px, py):
        app.popup = None if app.popup is self else self


class Checkbox(Widget):
    def __init__(self, x, y, label, on_change=None):
        super().__init__(x, y, 150, 22)
        self.label, self.checked, self.on_change = label, False, on_change

    def draw(self, c, app):
        c.create_rectangle(self.x, self.y + 3, self.x + 16, self.y + 19, fill=ACCENT if self.checked else WHITE,
                           outline=ACCENT if self.checked else "#8a8a8a")
        if self.checked:
            c.create_line(self.x + 3, self.y + 11, self.x + 7, self.y + 15, self.x + 13, self.y + 7, fill=WHITE, width=2)
        c.create_text(self.x + 24, self.y + 11, text=self.label, anchor="w", font=app.f_ui, fill=TEXT)

    def click(self, app, px, py):
        self.checked = not self.checked
        if self.on_change:
            app.with_delay(self.on_change)


class Grid(Widget):
    ROW, HEAD = 28, 32
    COLS = [("sku", "SKU", 96, "w"), ("name", "Name", 270, "w"), ("category", "Category", 120, "w"),
            ("on_hand", "On hand", 84, "e"), ("reorder_at", "Reorder at", 92, "e"),
            ("location", "Location", 92, "w"), ("price", "Price", 90, "e")]

    def __init__(self, app, x, y):
        super().__init__(x, y, sum(c[2] for c in self.COLS), self.HEAD + PAGE * self.ROW)
        self.app, self.page, self.sort, self.desc = app, 0, "sku", False
        self.selected, self.last_click = None, (None, 0.0)

    def rows(self):
        return self.app.filtered()

    def page_rows(self):
        rows = self.rows()
        return rows[self.page * PAGE:(self.page + 1) * PAGE]

    def pages(self):
        return max(1, math.ceil(len(self.rows()) / PAGE))

    def draw(self, c, app):
        c.create_rectangle(self.x, self.y, self.x + self.w, self.y + self.h, fill=WHITE, outline=LINE)
        c.create_rectangle(self.x, self.y, self.x + self.w, self.y + self.HEAD, fill="#fafafa", outline=LINE)
        cx = self.x
        for key, title, width, anchor in self.COLS:
            label = title + ((" ▼" if self.desc else " ▲") if key == self.sort else "")
            tx = cx + 10 if anchor == "w" else cx + width - 10
            c.create_text(tx, self.y + self.HEAD // 2, text=label, anchor=anchor, font=app.f_bold, fill=MUTED)
            cx += width
        if app.loading:
            self._spinner(c, app)
            return
        rows = self.page_rows()
        if not rows:
            c.create_text(self.x + self.w // 2, self.y + 120, text="No products match your filters.", font=app.f_ui, fill=MUTED)
        for i, p in enumerate(rows):
            ry = self.y + self.HEAD + i * self.ROW
            low = p["on_hand"] <= p["reorder_at"]
            fill = SEL if p["sku"] == self.selected else ("#fbfbfb" if i % 2 else WHITE)
            c.create_rectangle(self.x + 1, ry, self.x + self.w - 1, ry + self.ROW, fill=fill, outline="")
            c.create_line(self.x, ry + self.ROW, self.x + self.w, ry + self.ROW, fill="#efefef")
            cx = self.x
            for key, _, width, anchor in self.COLS:
                v = p[key]
                text = f"${v:,.2f}" if key == "price" else str(v)
                tx = cx + 10 if anchor == "w" else cx + width - 10
                color = DANGER if key == "on_hand" and low else TEXT
                c.create_text(tx, ry + self.ROW // 2, text=text, anchor=anchor, font=app.f_ui, fill=color)
                cx += width

    def _spinner(self, c, app):
        cx, cy = self.x + self.w // 2, self.y + 150
        start = (time.time() * 360) % 360
        c.create_arc(cx - 16, cy - 16, cx + 16, cy + 16, start=start, extent=270, style="arc", outline=ACCENT, width=3)
        c.create_text(cx, cy + 34, text="Loading...", font=app.f_ui, fill=MUTED)

    def click(self, app, px, py):
        if py < self.y + self.HEAD:
            cx = self.x
            for key, _, width, _ in self.COLS:
                if cx <= px < cx + width:
                    if self.sort == key:
                        self.desc = not self.desc
                    else:
                        self.sort, self.desc = key, False
                    self.page = 0
                    app.with_delay(lambda: None)
                    return
                cx += width
            return
        i = (py - self.y - self.HEAD) // self.ROW
        rows = self.page_rows()
        if 0 <= i < len(rows):
            sku = rows[i]["sku"]
            last_sku, last_t = self.last_click
            now = time.time()
            self.selected, self.last_click = sku, (sku, now)
            if last_sku == sku and now - last_t < 0.45:
                app.open_product(sku)

    def wheel(self, app, delta):
        new = min(max(0, self.page + (1 if delta < 0 else -1)), self.pages() - 1)
        if new != self.page:
            self.page = new
            app.with_delay(lambda: None)


# ============================================================================ dialogs
class Dialog:
    def __init__(self, title, w, h, cancel=None):
        self.title, self.w, self.h = title, w, h
        self.x, self.y = (W - w) // 2, (H - h) // 2
        self.widgets, self.labels, self.cancel, self.default = [], [], cancel, None
        self.error = ""

    def draw(self, c, app):
        c.create_rectangle(self.x + 4, self.y + 6, self.x + self.w + 4, self.y + self.h + 6, fill="#d0d0d0", outline="")
        c.create_rectangle(self.x, self.y, self.x + self.w, self.y + self.h, fill=WHITE, outline=ACCENT)
        c.create_rectangle(self.x, self.y, self.x + self.w, self.y + 4, fill=ACCENT, outline="")
        c.create_text(self.x + 24, self.y + 30, text=self.title, anchor="w", font=app.f_title2, fill=TEXT)
        for lx, ly, text, font, color in self.labels:
            c.create_text(self.x + lx, self.y + ly, text=text, anchor="w", font=getattr(app, font), fill=color)
        if self.error:
            c.create_text(self.x + 24, self.y + self.h - 78, text=self.error, anchor="w", font=app.f_ui, fill=DANGER)
        for w in self.widgets:
            w.draw(c, app)

    def place(self, widget):
        widget.x += self.x
        widget.y += self.y
        self.widgets.append(widget)
        return widget


# ============================================================================ app
class StockPilot:
    def __init__(self, fast=False):
        self.fast = fast
        self.root = tk.Tk()
        self.root.title("StockPilot Inventory Manager")
        self.root.geometry(f"{W}x{H}+20+20")
        self.root.resizable(False, False)
        self.c = tk.Canvas(self.root, width=W, height=H, bg=BG, highlightthickness=0, bd=0)
        self.c.pack()
        fam = "Segoe UI"
        self.f_ui = tkfont.Font(family=fam, size=9)
        self.f_bold = tkfont.Font(family=fam, size=9, weight="bold")
        self.f_side = tkfont.Font(family=fam, size=10)
        self.f_title = tkfont.Font(family="Segoe UI Light", size=18)
        self.f_title2 = tkfont.Font(family="Segoe UI Light", size=14)
        self.f_big = tkfont.Font(family="Segoe UI Light", size=26)

        self.data = self.load()
        self.view, self.screen = "dashboard", "splash"
        self.dialogs, self.popup, self.focus, self.hover = [], None, None, None
        self.loading, self.busy_until, self.caret_on = False, 0.0, True
        self.toast_text, self.toast_until = "", 0.0
        self.search_job = None
        self.build_products_view()

        self.c.bind("<Button-1>", self.on_click)
        self.c.bind("<Motion>", self.on_motion)
        self.c.bind("<MouseWheel>", self.on_wheel)
        self.root.bind("<Key>", self.on_key)
        self.root.protocol("WM_DELETE_WINDOW", self.root.destroy)
        self.root.after(500, self.blink)
        self.root.after(80, self.tick)
        self.root.after(0 if fast else random.randint(1500, 2500), self.ready)
        self.redraw()

    # ------------------------------------------------------------------ data
    def load(self):
        if not os.path.exists(DATA):
            shutil.copy(SEED, DATA)
        with open(DATA, encoding="utf-8") as fh:
            return json.load(fh)

    def save(self):
        with open(DATA, "w", encoding="utf-8") as fh:
            json.dump(self.data, fh, indent=1)

    def product(self, sku):
        return next((p for p in self.data["products"] if p["sku"] == sku), None)

    def filtered(self):
        q = self.search.value.strip().lower()
        rows = [p for p in self.data["products"]
                if (not q or q in p["sku"].lower() or q in p["name"].lower())
                and (self.category.value in (None, "All categories") or p["category"] == self.category.value)
                and (not self.low_only.checked or p["on_hand"] <= p["reorder_at"])]
        key = self.grid.sort
        rows.sort(key=lambda p: (p[key].lower() if isinstance(p[key], str) else p[key]), reverse=self.grid.desc)
        return rows

    # ------------------------------------------------------------------ timing
    def with_delay(self, fn):
        """Run fn after a short 'Loading...' spinner, ignoring input meanwhile."""
        if self.fast:
            fn()
            self.redraw()
            return
        self.loading, self.busy_until = True, time.time() + random.uniform(*DELAY)

        def done():
            self.loading = False
            fn()
            self.clamp_page()
            self.redraw()
        self.root.after(int((self.busy_until - time.time()) * 1000), done)

    def tick(self):
        if self.loading or self.toast_until:
            if self.toast_until and time.time() > self.toast_until:
                self.toast_until = 0.0
            self.redraw()
        self.root.after(80, self.tick)

    def blink(self):
        self.caret_on = not self.caret_on
        if isinstance(self.focus, TextBox):
            self.redraw()
        self.root.after(530, self.blink)

    def toast(self, text):
        self.toast_text, self.toast_until = text, time.time() + 3.5

    def ready(self):
        self.screen = "main"
        tip = Dialog("Tip of the day", 460, 210, cancel=lambda: self.close_dialog())
        tip.labels.append((24, 78, random.choice(TIPS), "f_ui", TEXT))
        tip.place(Checkbox(24, 112, "Don't show tips at startup"))
        close = tip.place(Button("Close", 340, 156, 96, 30, self.close_dialog, primary=True))
        tip.default = close
        self.dialogs.append(tip)
        self.redraw()

    # ------------------------------------------------------------------ views
    def build_products_view(self):
        self.search = TextBox(196, 104, 260, placeholder="Search name or SKU...", on_change=self.search_changed)
        self.category = Combo(470, 104, 180, ["All categories"] + CATEGORIES, value="All categories",
                              on_change=self.filter_changed)
        self.low_only = Checkbox(668, 107, "Low stock only", on_change=self.filter_changed)
        self.new_btn = Button("+ New product", 964, 103, 120, 30, self.new_product, primary=True)
        self.grid = Grid(self, 196, 146)
        self.prev_btn = Button("‹ Prev", 880, 616, 76, 28, lambda: self.go_page(-1))
        self.next_btn = Button("Next ›", 1008, 616, 76, 28, lambda: self.go_page(1))

    def view_widgets(self):
        if self.view == "products":
            return [self.search, self.category, self.low_only, self.new_btn, self.grid, self.prev_btn, self.next_btn]
        return []

    def nav(self, view):
        if view != self.view:
            self.popup = None
            self.focus = None
            self.with_delay(lambda: setattr(self, "view", view))

    def search_changed(self):
        if self.search_job:
            self.root.after_cancel(self.search_job)
        self.search_job = self.root.after(0 if self.fast else 450, lambda: self.with_delay(self.filter_changed))

    def filter_changed(self):
        self.grid.page, self.grid.selected = 0, None

    def go_page(self, d):
        new = min(max(0, self.grid.page + d), self.grid.pages() - 1)
        if new != self.grid.page:
            self.grid.page = new
            self.with_delay(lambda: None)

    def clamp_page(self):
        self.grid.page = min(self.grid.page, self.grid.pages() - 1)

    # ------------------------------------------------------------------ dialogs
    def close_dialog(self):
        if self.dialogs:
            self.dialogs.pop()
        self.focus = None
        self.redraw()

    def open_product(self, sku):
        p = self.product(sku)
        d = Dialog(f"Product details — {sku}", 540, 380, cancel=self.close_dialog)
        self.fill_product_labels(d, p)
        d.place(Button("Adjust stock...", 24, 320, 140, 32, lambda: self.adjust_stock(sku), primary=True))
        d.default = d.place(Button("Close", 420, 320, 96, 32, self.close_dialog))
        self.dialogs.append(d)
        self.redraw()

    def fill_product_labels(self, d, p):
        d.labels = [(24, 62, p["name"], "f_title2", TEXT)]
        rows = [("Category", p["category"]), ("Reorder at", str(p["reorder_at"])),
                ("Location", p["location"]), ("Price", f"${p['price']:,.2f}")]
        for i, (k, v) in enumerate(rows):
            d.labels += [(24, 110 + i * 30, k, "f_ui", MUTED), (130, 110 + i * 30, v, "f_ui", TEXT)]
        d.labels += [(330, 110, "On hand", "f_ui", MUTED),
                     (330, 150, str(p["on_hand"]), "f_big", DANGER if p["on_hand"] <= p["reorder_at"] else TEXT)]
        last = next((m for m in reversed(self.data["movements"]) if m["sku"] == p["sku"]), None)
        text = f"{last['change']:+d} ({last['reason']}) on {last['date']}" if last else "No movements recorded"
        d.labels += [(24, 250, "Last movement", "f_ui", MUTED), (130, 250, text, "f_ui", TEXT)]

    def adjust_stock(self, sku):
        p = self.product(sku)
        d = Dialog(f"Adjust stock — {sku}", 440, 330, cancel=self.close_dialog)
        d.labels = [(24, 70, f"Currently on hand: {p['on_hand']}", "f_ui", MUTED),
                    (24, 104, "Change (+/-)", "f_ui", TEXT), (24, 146, "Reason", "f_ui", TEXT),
                    (24, 188, "Note (optional)", "f_ui", TEXT)]
        change = d.place(TextBox(150, 90, 120, placeholder="e.g. -3"))
        reason = d.place(Combo(150, 132, 200, REASONS, placeholder="Select a reason..."))
        note = d.place(TextBox(150, 174, 266))

        def save():
            raw = change.value.strip().replace(" ", "")
            try:
                delta = int(raw)
                if delta == 0:
                    raise ValueError
            except ValueError:
                d.error = "Enter a whole number other than 0, for example 5 or -2."
                self.redraw()
                return
            if not reason.value:
                d.error = "Choose a reason for this change."
                self.redraw()
                return
            if p["on_hand"] + delta < 0:
                self.message("Can't save", f"Stock can't go below zero. On hand: {p['on_hand']}, change: {delta:+d}.")
                return

            def commit():
                p["on_hand"] += delta
                self.data["movements"].append({"sku": sku, "change": delta, "reason": reason.value,
                                               "note": note.value.strip(), "date": time.strftime("%m/%d/%Y %H:%M")})
                self.save()
                self.dialogs.pop()  # the adjust dialog; the product dialog stays, refreshed
                if self.dialogs:
                    self.fill_product_labels(self.dialogs[-1], p)
                self.toast(f"Stock updated: {sku} now {p['on_hand']}")
            self.with_delay(commit)

        d.place(Button("Save", 228, 278, 90, 32, save, primary=True))
        d.place(Button("Cancel", 326, 278, 90, 32, self.close_dialog))
        d.default = d.widgets[-2]
        self.dialogs.append(d)
        self.set_focus(change)

    def new_product(self):
        d = Dialog("New product", 520, 420, cancel=self.close_dialog)
        names = ["SKU", "Name", "Category", "Reorder at", "Location", "Price"]
        d.labels = [(24, 86 + i * 44, n, "f_ui", TEXT) for i, n in enumerate(names)]
        sku = d.place(TextBox(140, 72, 160, placeholder="e.g. FAS-2001"))
        name = d.place(TextBox(140, 116, 350))
        cat = d.place(Combo(140, 160, 200, CATEGORIES))
        reorder = d.place(TextBox(140, 204, 100))
        loc = d.place(TextBox(140, 248, 120, placeholder="e.g. A-01-1"))
        price = d.place(TextBox(140, 292, 120, placeholder="0.00"))

        def create():
            s, n = sku.value.strip().upper(), name.value.strip()
            if not s or not n or not cat.value or not reorder.value.strip() or not loc.value.strip() or not price.value.strip():
                d.error = "Fill in every field."
            elif self.product(s):
                d.error = f"SKU {s} already exists."
            else:
                try:
                    r, pr = int(reorder.value.strip()), float(price.value.strip().lstrip("$"))
                except ValueError:
                    d.error = "Reorder at must be a whole number and Price a number."
                else:
                    def commit():
                        self.data["products"].append({"sku": s, "name": n, "category": cat.value, "on_hand": 0,
                                                      "reorder_at": r, "location": loc.value.strip().upper(),
                                                      "price": round(pr, 2)})
                        self.save()
                        self.close_dialog()
                        self.toast(f"Product {s} created")
                    self.with_delay(commit)
                    return
            self.redraw()

        d.place(Button("Create", 304, 368, 96, 32, create, primary=True))
        d.place(Button("Cancel", 408, 368, 96, 32, self.close_dialog))
        d.default = d.widgets[-2]
        self.dialogs.append(d)
        self.set_focus(sku)

    def message(self, title, text):
        d = Dialog(title, 420, 180, cancel=self.close_dialog)
        d.labels = [(24, 76, text, "f_ui", TEXT)]
        d.default = d.place(Button("OK", 300, 128, 96, 30, self.close_dialog, primary=True))
        self.dialogs.append(d)
        self.redraw()

    # ------------------------------------------------------------------ input
    def set_focus(self, w):
        self.focus, self.caret_on = w, True
        self.redraw()

    def active_widgets(self):
        return self.dialogs[-1].widgets if self.dialogs else self.view_widgets()

    def busy(self):
        return self.loading or self.screen != "main"

    def on_click(self, e):
        if self.busy():
            return
        x, y = e.x, e.y
        if self.popup:
            if not self.popup.popup_click(self, x, y):
                self.popup = None
            self.redraw()
            return
        if not self.dialogs and x < 180 and y > 48:
            for i, view in enumerate(["dashboard", "products", "movements"]):
                if 64 + i * 44 <= y < 104 + i * 44:
                    self.nav(view)
            self.redraw()
            return
        hit = next((w for w in self.active_widgets() if w.contains(x, y) and w.enabled), None)
        if hit is None:
            if not self.dialogs:
                self.focus = None
            self.redraw()
            return
        if hit.focusable:
            hit.click(self, x, y)
        else:
            if not isinstance(hit, Grid):
                self.focus = None if not self.dialogs else self.focus
            hit.click(self, x, y)
        self.redraw()

    def on_motion(self, e):
        hit = next((w for w in self.active_widgets() if isinstance(w, Button) and w.contains(e.x, e.y)), None)
        if hit is not self.hover:
            self.hover = hit
            self.redraw()

    def on_wheel(self, e):
        if not self.busy() and not self.dialogs and self.view == "products" and self.grid.contains(e.x, e.y):
            self.grid.wheel(self, e.delta)

    def on_key(self, e):
        if self.busy():
            return
        ks = e.keysym
        if self.popup:
            if ks == "Escape":
                self.popup = None
                self.redraw()
            return
        if ks == "Escape" and self.dialogs and self.dialogs[-1].cancel:
            self.dialogs[-1].cancel()
            return
        if ks == "Return":
            if self.dialogs and self.dialogs[-1].default:
                self.dialogs[-1].default.cmd()
            elif not self.dialogs and self.view == "products" and self.grid.selected and self.focus is not self.search:
                self.open_product(self.grid.selected)
            return
        if ks in ("Tab", "ISO_Left_Tab"):
            fields = [w for w in self.active_widgets() if w.focusable]
            if fields:
                i = fields.index(self.focus) if self.focus in fields else -1
                step = -1 if e.state & 0x1 else 1
                self.set_focus(fields[(i + step) % len(fields)])
                if isinstance(self.focus, TextBox):
                    self.focus.all_selected = True
            return
        if isinstance(self.focus, TextBox) and self.focus in self.active_widgets():
            if self.focus.key(self, e):
                self.caret_on = True
                self.redraw()

    # ------------------------------------------------------------------ painting
    def redraw(self):
        c = self.c
        c.delete("all")
        c.create_rectangle(0, 0, W, H, fill=BG, outline="")
        if self.screen == "splash":
            c.create_rectangle(0, 0, W, H, fill=ACCENT, outline="")
            c.create_text(W // 2, H // 2 - 20, text="StockPilot", font=self.f_big, fill=WHITE)
            c.create_text(W // 2, H // 2 + 22, text="Loading inventory...", font=self.f_side, fill="#dce6f4")
            return
        self.draw_chrome(c)
        if self.view == "dashboard":
            self.draw_dashboard(c)
        elif self.view == "products":
            self.draw_products(c)
        else:
            self.draw_movements(c)
        if self.popup and not self.dialogs:
            self.popup.draw_popup(c, self)
        for d in self.dialogs:
            c.create_rectangle(0, 0, W, H, fill="#808080", outline="", stipple="gray25")
            d.draw(c, self)
        if self.popup and self.dialogs:
            self.popup.draw_popup(c, self)
        if self.toast_until:
            tw = self.f_ui.measure(self.toast_text) + 40
            c.create_rectangle(W - tw - 20, H - 70, W - 20, H - 30, fill=TOAST, outline="")
            c.create_text(W - tw, H - 50, text="✓  " + self.toast_text, anchor="w", font=self.f_ui, fill=WHITE)

    def draw_chrome(self, c):
        c.create_rectangle(0, 0, 180, H, fill=SIDE, outline="")
        c.create_text(20, 26, text="StockPilot", anchor="w", font=self.f_title2, fill=WHITE)
        for i, (view, label) in enumerate([("dashboard", "Dashboard"), ("products", "Products"),
                                           ("movements", "Stock movements"), (None, "Reports")]):
            y = 64 + i * 44
            if view == self.view:
                c.create_rectangle(0, y, 180, y + 40, fill=SIDE_SEL, outline="")
                c.create_rectangle(0, y, 4, y + 40, fill=WHITE, outline="")
            c.create_text(22, y + 20, text=label, anchor="w", font=self.f_side, fill=WHITE if view else "#8fa8cf")
        c.create_text(20, H - 22, text="Warehouse: Main · v4.2", anchor="w", font=self.f_ui, fill="#c5d3ea")

    def draw_dashboard(self, c):
        c.create_text(196, 46, text="Dashboard", anchor="w", font=self.f_title, fill=TEXT)
        products = self.data["products"]
        low = [p for p in products if p["on_hand"] <= p["reorder_at"]]
        cards = [("Products", str(len(products))), ("Low stock", str(len(low))),
                 ("Units on hand", f"{sum(p['on_hand'] for p in products):,}")]
        for i, (k, v) in enumerate(cards):
            x = 196 + i * 296
            c.create_rectangle(x, 84, x + 280, 184, fill=WHITE, outline=LINE)
            c.create_text(x + 20, 108, text=k, anchor="w", font=self.f_ui, fill=MUTED)
            c.create_text(x + 20, 150, text=v, anchor="w", font=self.f_big, fill=DANGER if k == "Low stock" else TEXT)
        c.create_text(196, 214, text="Recent stock movements", anchor="w", font=self.f_title2, fill=TEXT)
        moves = list(reversed(self.data["movements"]))[:8]
        if not moves:
            c.create_text(196, 250, text="No stock movements yet. Open Products to adjust stock.", anchor="w",
                          font=self.f_ui, fill=MUTED)
        for i, m in enumerate(moves):
            c.create_text(196, 250 + i * 26, text=f"{m['date']}   {m['sku']}   {m['change']:+d}   {m['reason']}",
                          anchor="w", font=self.f_ui, fill=TEXT)

    def draw_products(self, c):
        c.create_text(196, 46, text="Products", anchor="w", font=self.f_title, fill=TEXT)
        for w in self.view_widgets():
            w.draw(c, self)
        rows = self.grid.rows()
        start = self.grid.page * PAGE
        shown = f"Showing {start + 1}–{min(start + PAGE, len(rows))} of {len(rows)}" if rows else "Showing 0 of 0"
        c.create_text(196, 630, text=shown, anchor="w", font=self.f_ui, fill=MUTED)
        c.create_text(982, 630, text=f"{self.grid.page + 1} / {self.grid.pages()}", font=self.f_ui, fill=TEXT)

    def draw_movements(self, c):
        c.create_text(196, 46, text="Stock movements", anchor="w", font=self.f_title, fill=TEXT)
        cols = [("Date", 196), ("SKU", 356), ("Change", 466), ("Reason", 556), ("Note", 716)]
        c.create_rectangle(196, 84, 1084, 116, fill="#fafafa", outline=LINE)
        for title, x in cols:
            c.create_text(x + 10, 100, text=title, anchor="w", font=self.f_bold, fill=MUTED)
        moves = list(reversed(self.data["movements"]))[:18]
        if not moves:
            c.create_text(206, 140, text="No stock movements recorded.", anchor="w", font=self.f_ui, fill=MUTED)
        for i, m in enumerate(moves):
            y = 132 + i * 28
            vals = [m["date"], m["sku"], f"{m['change']:+d}", m["reason"], m["note"]]
            for (_, x), v in zip(cols, vals):
                c.create_text(x + 10, y, text=v, anchor="w", font=self.f_ui, fill=TEXT)


def main():
    if "--reset" in sys.argv[1:] and os.path.exists(DATA):
        os.remove(DATA)
    StockPilot(fast="--fast" in sys.argv[1:]).root.mainloop()


if __name__ == "__main__":
    main()

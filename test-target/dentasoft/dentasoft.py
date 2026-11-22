#!/usr/bin/env python3
"""
DentaSoft Patient Manager v2.3  (c) 1998 Molar Systems Inc.

A deliberately old, laggy, automation-hostile test target for a computer-use agent.
Every control is painted onto ONE Tk Canvas: there are no native widgets, so nothing
is exposed to UI Automation / accessibility tools. The only interface is pixels in,
mouse and keyboard out.

Usage:
    python dentasoft.py            # run (creates patients.json from the seed on first run)
    python dentasoft.py --reset    # restore patients.json from patients.seed.json, then run
    python dentasoft.py --fast     # disable artificial lag (for development only)

Login:  user "drsmith"  password "molar123"
"""
import json
import os
import random
import shutil
import sys
import time
import tkinter as tk
from tkinter import font as tkfont

HERE = os.path.dirname(os.path.abspath(__file__))
SEED = os.path.join(HERE, "patients.seed.json")
DATA = os.path.join(HERE, "patients.json")

W, H = 800, 600
BG, HI, SH, DK, TXT = "#c0c0c0", "#ffffff", "#808080", "#404040", "#000000"
NAVY, FIELD_BG, DISABLED = "#000080", "#ffffff", "#808080"
LAG = (0.35, 1.2)          # seconds of "Please wait..." per action
PAINT_LAG = (0.25, 0.8)    # seconds a screen stays half-painted after a transition
USERS = {"drsmith": "molar123"}

EDITABLE = ["phone", "insurance", "policy", "balance", "next_appt", "allergies", "notes"]
FIELD_DEFS = [  # (key, label, editable)
    ("id", "Patient ID:", False), ("last", "Last Name:", False), ("first", "First Name:", False),
    ("dob", "Date of Birth:", False), ("phone", "Phone:", True), ("insurance", "Insurance:", True),
    ("policy", "Policy #:", True), ("balance", "Balance Due:", True), ("last_visit", "Last Visit:", False),
    ("next_appt", "Next Appt:", True), ("allergies", "Allergies:", True), ("notes", "Notes:", True),
]


# ----------------------------------------------------------------------------- drawing helpers
def raised(c, x, y, w, h, fill=BG):
    c.create_rectangle(x, y, x + w, y + h, fill=fill, outline="")
    c.create_line(x, y + h - 1, x, y, x + w - 1, y, fill=HI)
    c.create_line(x + w - 1, y, x + w - 1, y + h - 1, x, y + h - 1, fill=DK)
    c.create_line(x + w - 2, y + 1, x + w - 2, y + h - 2, x + 1, y + h - 2, fill=SH)


def sunken(c, x, y, w, h, fill=FIELD_BG):
    c.create_rectangle(x, y, x + w, y + h, fill=fill, outline="")
    c.create_line(x, y + h - 1, x, y, x + w - 1, y, fill=SH)
    c.create_line(x + 1, y + h - 2, x + 1, y + 1, x + w - 2, y + 1, fill=DK)
    c.create_line(x + w - 1, y, x + w - 1, y + h - 1, x, y + h - 1, fill=HI)
    c.create_line(x + w - 2, y + 1, x + w - 2, y + h - 2, x + 1, y + h - 2, fill=BG)


def groupbox(c, x, y, w, h, title, font):
    c.create_rectangle(x, y + 6, x + w - 1, y + h - 1, outline=SH)
    c.create_rectangle(x + 1, y + 7, x + w, y + h, outline=HI)
    tw = font.measure(title) + 6
    c.create_rectangle(x + 8, y, x + 8 + tw, y + 12, fill=BG, outline="")
    c.create_text(x + 11, y + 6, text=title, anchor="w", font=font, fill=TXT)


# ----------------------------------------------------------------------------- widgets
class Widget:
    def __init__(self, x, y, w, h):
        self.x, self.y, self.w, self.h = x, y, w, h

    def contains(self, px, py):
        return self.x <= px < self.x + self.w and self.y <= py < self.y + self.h


class Field(Widget):
    def __init__(self, key, x, y, w, h=20, value="", editable=True, password=False, maxlen=60):
        super().__init__(x, y, w, h)
        self.name, self.value, self.editable, self.password, self.maxlen = key, value, editable, password, maxlen

    def draw(self, c, app):
        sunken(c, self.x, self.y, self.w, self.h, fill=FIELD_BG if self.editable else BG)
        shown = "*" * len(self.value) if self.password else self.value
        f = app.f_field
        while f.measure(shown) > self.w - 8 and shown:
            shown = shown[1:]
        ty = self.y + 11 if self.h > 24 else self.y + self.h // 2  # multi-line boxes top-align
        c.create_text(self.x + 4, ty, text=shown, anchor="w", font=f, fill=TXT)
        if app.focus is self and self.editable and app.caret_on and not app.modal:
            cx = self.x + 4 + f.measure(shown) + 1
            c.create_line(cx, ty - 7, cx, ty + 7, fill=TXT)

    def key(self, keysym, char):
        if keysym == "BackSpace":
            self.value = self.value[:-1]
        elif char and char.isprintable() and len(self.value) < self.maxlen:
            self.value += char


class Button(Widget):
    def __init__(self, label, x, y, w, h, cmd, enabled=True, default=False):
        super().__init__(x, y, w, h)
        self.label, self.cmd, self.enabled, self.default = label, cmd, enabled, default

    def draw(self, c, app):
        if self.default and self.enabled:
            c.create_rectangle(self.x, self.y, self.x + self.w, self.y + self.h, fill=TXT, outline="")
            raised(c, self.x + 1, self.y + 1, self.w - 2, self.h - 2)
        else:
            raised(c, self.x, self.y, self.w, self.h)
        cx, cy = self.x + self.w // 2, self.y + self.h // 2
        if self.enabled:
            c.create_text(cx, cy, text=self.label, font=app.f_ui, fill=TXT)
        else:  # classic embossed disabled text
            c.create_text(cx + 1, cy + 1, text=self.label, font=app.f_ui, fill=HI)
            c.create_text(cx, cy, text=self.label, font=app.f_ui, fill=SH)


class ListBox(Widget):
    ROW = 15

    def __init__(self, x, y, w, h, on_select):
        super().__init__(x, y, w, h)
        self.rows, self.selected, self.on_select = [], None, on_select

    def draw(self, c, app):
        sunken(c, self.x, self.y, self.w, self.h)
        if app.half_painted:
            return
        for i, row in enumerate(self.rows):
            ry = self.y + 3 + i * self.ROW
            if ry + self.ROW > self.y + self.h - 2:
                break
            if i == self.selected:
                c.create_rectangle(self.x + 3, ry, self.x + self.w - 3, ry + self.ROW, fill=NAVY, outline="")
            c.create_text(self.x + 6, ry + self.ROW // 2, text=row, anchor="w", font=app.f_mono,
                          fill=HI if i == self.selected else TXT)

    def click(self, px, py):
        i = (py - self.y - 3) // self.ROW
        if 0 <= i < len(self.rows):
            self.selected = i
            self.on_select(i)


# ----------------------------------------------------------------------------- application
class DentaSoft:
    def __init__(self, fast=False):
        self.fast = fast
        self.root = tk.Tk()
        self.root.title("DentaSoft Patient Manager v2.3")
        self.root.geometry(f"{W}x{H}+120+80")
        self.root.resizable(False, False)
        self.root.configure(bg=BG)
        self.c = tk.Canvas(self.root, width=W, height=H, bg=BG, highlightthickness=0, bd=0)
        self.c.pack()
        self.f_ui = tkfont.Font(family="MS Sans Serif", size=8)
        self.f_bold = tkfont.Font(family="MS Sans Serif", size=8, weight="bold")
        self.f_field = tkfont.Font(family="MS Sans Serif", size=9)
        self.f_mono = tkfont.Font(family="Courier New", size=9)
        self.f_title = tkfont.Font(family="Times New Roman", size=22, weight="bold")

        self.patients = self.load_data()
        self.screen = "splash"
        self.widgets, self.fields = [], []
        self.focus, self.caret_on = None, True
        self.modal, self.open_menu = None, None
        self.busy, self.half_painted = False, False
        self.status, self.user = "Loading patient database...", ""
        self.results, self.current, self.editing, self.original = [], None, False, None
        self.menus = [
            ("File", [("Exit", self.ask_exit)]),
            ("Patients", [("Find Patient...     F3", self.focus_find), ("New Patient...", None), ("Delete Patient", None)]),
            ("Reports", [("Daily Schedule", None), ("Outstanding Balances", None), ("Recall List", None)]),
            ("Help", [("Contents", None), ("About DentaSoft...", self.about)]),
        ]

        self.c.bind("<Button-1>", self.on_click)
        self.root.bind("<Key>", self.on_key)
        self.root.protocol("WM_DELETE_WINDOW", self.ask_exit)
        self.root.after(500, self.blink)
        self.root.after(0 if fast else random.randint(1800, 3000), self.enter_login)
        self.redraw()

    # ---------------------------------------------------------------- data
    def load_data(self):
        if not os.path.exists(DATA):
            shutil.copy(SEED, DATA)
        with open(DATA, encoding="utf-8") as fh:
            return json.load(fh)

    def save_data(self):
        with open(DATA, "w", encoding="utf-8") as fh:
            json.dump(self.patients, fh, indent=2)

    # ---------------------------------------------------------------- lag simulation
    def lagged(self, fn, status="Please wait..."):
        """Run fn after a random delay, blocking input and showing an hourglass meanwhile."""
        if self.fast:
            fn()
            self.redraw()
            return
        self.busy, self.status = True, status
        self.c.config(cursor="watch")
        self.redraw()

        def done():
            self.busy = False
            self.c.config(cursor="arrow")
            if self.status == status:
                self.status = "Ready"
            fn()
            self.redraw()
        self.root.after(int(random.uniform(*LAG) * 1000), done)

    def slow_paint(self):
        """Leave the screen half-painted for a moment, like a slow GDI repaint."""
        if self.fast:
            return
        self.half_painted = True

        def finish():
            self.half_painted = False
            self.redraw()
        self.root.after(int(random.uniform(*PAINT_LAG) * 1000), finish)

    def blink(self):
        self.caret_on = not self.caret_on
        if self.focus is not None and not self.busy:
            self.redraw()
        self.root.after(500, self.blink)

    # ---------------------------------------------------------------- screens
    def enter_login(self):
        self.screen, self.status, self.modal = "login", "", None
        self.u = Field("user", 330, 262, 180, value="")
        self.p = Field("pass", 330, 292, 180, value="", password=True)
        self.fields = [self.u, self.p]
        self.widgets = [self.u, self.p,
                        Button("OK", 310, 335, 85, 24, self.try_login, default=True),
                        Button("Cancel", 410, 335, 85, 24, self.root.destroy)]
        self.focus = self.u
        self.slow_paint()
        self.redraw()

    def try_login(self):
        def check():
            if USERS.get(self.u.value.strip().lower()) == self.p.value:
                self.user = self.u.value.strip().lower()
                self.lagged(self.enter_main, "Loading patient database...")
            else:
                self.p.value = ""
                self.focus = self.u
                self.msgbox("Login Failed", "Invalid user name or password.\nPlease try again.")
        self.lagged(check, "Verifying...")

    def enter_main(self):
        self.screen, self.status = "main", "Ready"
        self.editing, self.current, self.results = False, None, []
        self.find_id = Field("find_id", 90, 100, 120)
        self.find_last = Field("find_last", 90, 128, 120)
        self.list = ListBox(26, 186, 214, 352, self.select_result)
        self.detail = {}
        y = 100
        for key, label, editable in FIELD_DEFS:
            h = 60 if key == "notes" else 20
            self.detail[key] = Field(key, 380, y, 370, h, value="", editable=False)
            y += h + 8
        self.b_find = Button("Find", 160, 154, 70, 22, self.do_find, default=True)
        self.b_edit = Button("Edit", 380, 545, 80, 24, self.start_edit, enabled=False)
        self.b_save = Button("Save", 470, 545, 80, 24, self.do_save, enabled=False)
        self.b_cancel = Button("Cancel", 560, 545, 80, 24, self.cancel_edit, enabled=False)
        self.widgets = [self.find_id, self.find_last, self.list, self.b_find, self.b_edit, self.b_save, self.b_cancel]
        self.widgets += list(self.detail.values())
        self.fields = [self.find_id, self.find_last]
        self.focus = self.find_id
        self.slow_paint()
        self.redraw()

    # ---------------------------------------------------------------- main-screen actions
    def focus_find(self):
        self.focus = self.find_id

    def do_find(self):
        if self.editing:
            self.msgbox("DentaSoft", "Finish editing the current record before searching.")
            return
        pid, last = self.find_id.value.strip().upper(), self.find_last.value.strip().lower()

        def run():
            if pid:
                self.results = [p for p in self.patients if p["id"].upper() == pid]
            elif last:
                self.results = [p for p in self.patients if p["last"].lower().startswith(last)]
            else:
                self.results = []
                self.msgbox("DentaSoft", "Enter a Patient ID or a Last Name to search.")
                return
            self.results.sort(key=lambda p: (p["last"], p["first"]))
            self.list.rows = [f"{p['id']}  {p['last']}, {p['first']}" for p in self.results]
            self.list.selected = None
            self.show_patient(None)
            self.slow_paint()
            if not self.results:
                self.msgbox("DentaSoft", "No patient records found matching\nyour search criteria.")
        self.lagged(run, "Searching...")

    def select_result(self, i):
        if self.editing:
            self.list.selected = self.results.index(self.current) if self.current in self.results else None
            self.msgbox("DentaSoft", "Finish editing the current record first.")
            return
        self.lagged(lambda: self.show_patient(self.results[i]), "Loading record...")

    def show_patient(self, p):
        self.current = p
        for key, _, _ in FIELD_DEFS:
            v = "" if p is None else p[key]
            if key == "balance" and p is not None:
                v = f"${v:,.2f}"
            self.detail[key].value = str(v)
            self.detail[key].editable = False
        self.b_edit.enabled = p is not None
        self.b_save.enabled = self.b_cancel.enabled = False
        self.fields = [self.find_id, self.find_last]
        self.status = "Ready" if p is None else f"Record {p['id']} loaded."

    def start_edit(self):
        if self.current is None:
            return
        self.editing = True
        for k in EDITABLE:
            self.detail[k].editable = True
        self.detail["balance"].value = f"{self.current['balance']:.2f}"
        self.original = {k: self.detail[k].value for k in EDITABLE}
        self.fields = [self.detail[k] for k in EDITABLE]
        self.focus = self.detail["phone"]
        self.b_edit.enabled = False
        self.b_save.enabled = self.b_cancel.enabled = True
        self.b_find.default, self.b_save.default = False, True
        self.status = "EDIT MODE - press Save to commit changes."
        self.redraw()

    def dirty(self):
        return self.editing and any(self.detail[k].value != self.original[k] for k in EDITABLE)

    def end_edit(self):
        self.editing = False
        self.b_find.default, self.b_save.default = True, False
        self.show_patient(self.current)
        self.focus = self.find_id

    def do_save(self):
        if not self.editing:
            return
        if not self.dirty():
            self.msgbox("DentaSoft", "No changes to save.")
            return
        raw = self.detail["balance"].value.replace("$", "").replace(",", "").strip()
        try:
            bal = round(float(raw), 2)
        except ValueError:
            self.msgbox("Invalid Entry", "Balance Due must be a dollar amount\n(for example 125.50).")
            self.focus = self.detail["balance"]
            return

        def commit():
            for k in EDITABLE:
                self.current[k] = bal if k == "balance" else self.detail[k].value.strip()
            self.save_data()
            self.end_edit()
            self.slow_paint()
            self.msgbox("DentaSoft", f"Patient record {self.current['id']} saved\nsuccessfully.")
        self.confirm("Confirm Save", f"Save changes to patient record\n{self.current['id']}?",
                     lambda: self.lagged(commit, "Saving..."))

    def cancel_edit(self):
        if not self.editing:
            return
        if self.dirty():
            self.confirm("DentaSoft", "Discard unsaved changes?", self.end_edit)
        else:
            self.end_edit()

    def ask_exit(self):
        if self.busy:
            return
        if self.dirty():
            self.confirm("DentaSoft", "You have unsaved changes.\nExit anyway?", self.root.destroy)
        else:
            self.confirm("DentaSoft", "Exit DentaSoft Patient Manager?", self.root.destroy)

    def about(self):
        self.msgbox("About DentaSoft", "DentaSoft Patient Manager\nVersion 2.3 (build 1147)\n\n(c) 1998 Molar Systems Inc.\nAll rights reserved.")

    # ---------------------------------------------------------------- modals
    def msgbox(self, title, text):
        self.modal = {"title": title, "text": text, "w": 340, "h": 150}
        self.modal["buttons"] = [Button("OK", 0, 0, 75, 23, self.close_modal, default=True)]
        self.layout_modal()
        self.redraw()

    def confirm(self, title, text, on_yes):
        self.modal = {"title": title, "text": text, "w": 340, "h": 150}

        def yes():
            self.close_modal()
            on_yes()
        self.modal["buttons"] = [Button("Yes", 0, 0, 75, 23, yes, default=True),
                                 Button("No", 0, 0, 75, 23, self.close_modal)]
        self.layout_modal()
        self.redraw()

    def layout_modal(self):
        m = self.modal
        m["x"], m["y"] = (W - m["w"]) // 2, (H - m["h"]) // 2
        bs = m["buttons"]
        total = len(bs) * 75 + (len(bs) - 1) * 10
        bx = m["x"] + (m["w"] - total) // 2
        for b in bs:
            b.x, b.y = bx, m["y"] + m["h"] - 36
            bx += 85

    def close_modal(self):
        self.modal = None
        self.redraw()

    # ---------------------------------------------------------------- input
    def on_click(self, e):
        if self.busy:
            self.root.bell()
            return
        x, y = e.x, e.y
        if self.modal:
            for b in self.modal["buttons"]:
                if b.contains(x, y):
                    b.cmd()
                    return
            self.root.bell()
            return
        if self.open_menu is not None:
            self.menu_click(x, y)
            return
        if self.screen == "main" and y < 20 and x < 300:
            self.open_menu = self.menu_at(x)
            self.redraw()
            return
        for w in self.widgets:
            if w.contains(x, y):
                if isinstance(w, Field):
                    if w.editable:
                        self.focus = w
                elif isinstance(w, Button):
                    if w.enabled:
                        w.cmd()
                        return
                elif isinstance(w, ListBox):
                    w.click(x, y)
                    return
                break
        self.redraw()

    def menu_at(self, x):
        mx = 6
        for i, (name, _) in enumerate(self.menus):
            mw = self.f_ui.measure(name) + 14
            if mx <= x < mx + mw:
                return i
            mx += mw
        return None

    def menu_click(self, x, y):
        name, items = self.menus[self.open_menu]
        mx = 6 + sum(self.f_ui.measure(n) + 14 for n, _ in self.menus[:self.open_menu])
        top, iw = 20, 170
        if mx <= x < mx + iw and top <= y < top + 4 + len(items) * 18:
            i = (y - top - 2) // 18
            if 0 <= i < len(items):
                label, cmd = items[i]
                self.open_menu = None
                if cmd:
                    cmd()
                self.redraw()
                return
        if y < 20:
            self.open_menu = self.menu_at(x)
        else:
            self.open_menu = None
        self.redraw()

    def on_key(self, e):
        if self.busy:
            return
        ks = e.keysym
        if self.modal:
            bs = self.modal["buttons"]
            if ks == "Return":
                bs[0].cmd()
            elif ks == "Escape":
                bs[-1].cmd()
            elif ks.lower() == "y" and len(bs) == 2:
                bs[0].cmd()
            elif ks.lower() == "n" and len(bs) == 2:
                bs[1].cmd()
            return
        if self.open_menu is not None:
            if ks == "Escape":
                self.open_menu = None
                self.redraw()
            return
        if ks == "Tab":
            if self.fields:
                i = self.fields.index(self.focus) if self.focus in self.fields else -1
                self.focus = self.fields[(i + 1) % len(self.fields)]
                self.caret_on = True
            self.redraw()
            return
        if ks == "Return":
            for w in self.widgets:
                if isinstance(w, Button) and w.default and w.enabled:
                    w.cmd()
                    return
            return
        if self.screen == "main":
            if ks == "F3":
                self.focus_find()
                self.redraw()
                return
            if ks == "Escape" and self.editing:
                self.cancel_edit()
                return
        if self.focus is not None and self.focus.editable:
            self.focus.key(ks, e.char)
            self.caret_on = True
            self.redraw()

    # ---------------------------------------------------------------- painting
    def redraw(self):
        c = self.c
        c.delete("all")
        c.create_rectangle(0, 0, W, H, fill=BG, outline="")
        if self.screen == "splash":
            self.draw_splash()
        elif self.screen == "login":
            self.draw_login()
        else:
            self.draw_main()
        if self.modal:
            self.draw_modal()

    def draw_splash(self):
        c = self.c
        raised(c, 200, 200, 400, 200)
        c.create_rectangle(210, 210, 590, 300, fill=NAVY, outline="")
        c.create_text(400, 245, text="DentaSoft", font=self.f_title, fill=HI)
        c.create_text(400, 280, text="Patient Manager v2.3", font=self.f_bold, fill=HI)
        c.create_text(400, 330, text=self.status, font=self.f_ui, fill=TXT)
        sunken(c, 230, 350, 340, 16)
        c.create_rectangle(233, 353, 233 + random.randint(80, 320), 363, fill=NAVY, outline="")
        c.create_text(400, 385, text="(c) 1998 Molar Systems Inc.", font=self.f_ui, fill=SH)

    def draw_login(self):
        c = self.c
        raised(c, 250, 180, 300, 200)
        c.create_rectangle(252, 182, 548, 200, fill=NAVY, outline="")
        c.create_text(258, 191, text="DentaSoft Login", anchor="w", font=self.f_bold, fill=HI)
        c.create_text(400, 230, text="Please enter your user name and password", font=self.f_ui, fill=TXT)
        c.create_text(270, 272, text="User name:", anchor="w", font=self.f_ui, fill=TXT)
        c.create_text(270, 302, text="Password:", anchor="w", font=self.f_ui, fill=TXT)
        if not self.half_painted:
            for w in self.widgets:
                w.draw(c, self)

    def draw_main(self):
        c = self.c
        # menu bar
        c.create_rectangle(0, 0, W, 20, fill=BG, outline="")
        c.create_line(0, 20, W, 20, fill=SH)
        mx = 6
        for i, (name, _) in enumerate(self.menus):
            mw = self.f_ui.measure(name) + 14
            if self.open_menu == i:
                c.create_rectangle(mx, 2, mx + mw, 19, fill=NAVY, outline="")
            c.create_text(mx + 7, 10, text=name, anchor="w", font=self.f_ui, fill=HI if self.open_menu == i else TXT)
            mx += mw
        # toolbar strip (decorative, like the real thing)
        raised(c, 0, 21, W, 30)
        for j, lbl in enumerate(["Find", "New", "Del", "Appt", "Bill", "Print"]):
            raised(c, 6 + j * 44, 25, 40, 22)
            c.create_text(26 + j * 44, 36, text=lbl, font=self.f_ui, fill=SH if j else TXT)
        c.create_text(W - 8, 36, text="DentaSoft Patient Manager", anchor="e", font=self.f_bold, fill=SH)
        # panels
        groupbox(c, 14, 66, 238, 482, "Find Patient", self.f_ui)
        groupbox(c, 268, 66, 518, 482, "Patient Record", self.f_ui)
        c.create_text(26, 110, text="Patient ID:", anchor="w", font=self.f_ui, fill=TXT)
        c.create_text(26, 138, text="Last Name:", anchor="w", font=self.f_ui, fill=TXT)
        c.create_text(26, 176, text="Results:", anchor="w", font=self.f_ui, fill=TXT)
        for key, label, _ in FIELD_DEFS:
            f = self.detail[key]
            c.create_text(282, f.y + 10, text=label, anchor="w", font=self.f_ui, fill=TXT)
        if self.editing:
            c.create_text(282, 557, text="* editing *", anchor="w", font=self.f_bold, fill="#800000")
        # widgets
        if not self.half_painted:
            for w in self.widgets:
                w.draw(c, self)
        else:
            for w in self.widgets:
                if isinstance(w, (Field, ListBox)):
                    sunken(c, w.x, w.y, w.w, w.h, fill=BG)
        # status bar
        sunken(c, 2, H - 22, 560, 20, fill=BG)
        c.create_text(8, H - 12, text=self.status, anchor="w", font=self.f_ui, fill=TXT)
        sunken(c, 566, H - 22, 120, 20, fill=BG)
        c.create_text(572, H - 12, text=f"User: {self.user}", anchor="w", font=self.f_ui, fill=TXT)
        sunken(c, 690, H - 22, 108, 20, fill=BG)
        c.create_text(696, H - 12, text=time.strftime("%m/%d/%Y"), anchor="w", font=self.f_ui, fill=TXT)
        # dropdown menu
        if self.open_menu is not None:
            name, items = self.menus[self.open_menu]
            mx = 6 + sum(self.f_ui.measure(n) + 14 for n, _ in self.menus[:self.open_menu])
            raised(c, mx, 20, 170, 4 + len(items) * 18)
            for i, (label, cmd) in enumerate(items):
                iy = 22 + i * 18
                if cmd:
                    c.create_text(mx + 18, iy + 9, text=label, anchor="w", font=self.f_ui, fill=TXT)
                else:
                    c.create_text(mx + 19, iy + 10, text=label, anchor="w", font=self.f_ui, fill=HI)
                    c.create_text(mx + 18, iy + 9, text=label, anchor="w", font=self.f_ui, fill=SH)

    def draw_modal(self):
        c, m = self.c, self.modal
        raised(c, m["x"], m["y"], m["w"], m["h"])
        c.create_rectangle(m["x"] + 2, m["y"] + 2, m["x"] + m["w"] - 2, m["y"] + 20, fill=NAVY, outline="")
        c.create_text(m["x"] + 8, m["y"] + 11, text=m["title"], anchor="w", font=self.f_bold, fill=HI)
        c.create_text(m["x"] + m["w"] // 2, m["y"] + 62, text=m["text"], font=self.f_ui, fill=TXT, justify="center")
        for b in m["buttons"]:
            b.draw(c, self)


def main():
    args = sys.argv[1:]
    if "--reset" in args and os.path.exists(DATA):
        os.remove(DATA)
    DentaSoft(fast="--fast" in args).root.mainloop()


if __name__ == "__main__":
    main()

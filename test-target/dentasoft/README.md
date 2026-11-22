# DentaSoft Patient Manager v2.3

A fake 1998-era dental office patient records program used as the test target for the
computer-use agent. It is built to be the kind of software only a vision-based agent can
automate:

- **No accessibility tree.** Every control is painted on a single Tk Canvas. UI Automation,
  Inspect.exe and AutoHotkey see one blank window.
- **Laggy.** Every action shows "Please wait..." with an hourglass for 0.35-1.2 s and
  ignores input meanwhile. Screens stay half-painted for a moment after transitions.
- **Old.** Fixed 800x600 window, 8 pt MS Sans Serif, bevelled Windows 95 chrome, modal
  dialogs for everything, an "unsaved changes" prompt on exit.

## Run

```
python dentasoft.py            # normal
python dentasoft.py --reset    # restore patients.json from the seed first
python dentasoft.py --fast     # no artificial lag (development only)
```

Requires only a standard Windows Python install (Tk is included).

Login: user `drsmith`, password `molar123`.

## Data

`patients.seed.json` is the fixed starting dataset (12 patients, IDs `P-1001` to `P-1012`).
On first run it is copied to `patients.json`, which the app reads and writes. Run with
`--reset` or delete `patients.json` to get back to a known state.

## Workflows an agent can be asked to do

1. **Look up a patient** (read-only). Type an ID into *Patient ID* (or a last-name prefix
   into *Last Name*), click *Find* or press Enter, click the row in *Results*, and read the
   *Patient Record* panel. Two patients share the last name Dawson.
2. **Update a record** (write). Select a patient, click *Edit*, change any of Phone,
   Insurance, Policy #, Balance Due, Next Appt, Allergies, Notes, click *Save*, answer
   *Yes* to the confirmation, dismiss the "saved successfully" box. A non-numeric balance
   is rejected with an error dialog. *Cancel* with changes asks to discard.

## Keyboard

Tab cycles fields, Enter presses the default (bold-bordered) button, Escape cancels an edit
or closes a menu, F3 jumps to the Patient ID box, Y/N answer Yes/No dialogs.

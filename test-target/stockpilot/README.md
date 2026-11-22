# StockPilot Inventory Manager 2014

A fake early-2010s inventory program, used as the second test target for the
computer-use agent. It is a decade newer than DentaSoft, so it exercises different
behaviour while staying just as opaque:

- **No accessibility tree.** Every control is painted on a single Tk Canvas.
- **2010s interface.** Flat Office-blue styling, sidebar navigation, a searchable,
  sortable product grid with 15 rows per page, dropdowns, a checkbox filter, stacked
  dialogs, toasts that disappear after a few seconds, and a "Tip of the day" popup at
  startup that has to be closed first.
- **Modern text boxes.** Unlike DentaSoft, Ctrl+A, Home, End, Delete and the arrow keys
  work, so an agent shouldn't carry over DentaSoft's backspace-only habits.
- **Short delays.** Searches, filters, paging and saves show a spinner for 0.2-0.7 s and
  ignore input meanwhile; the search box filters 0.45 s after typing stops.

## Run

```
python stockpilot.py            # normal
python stockpilot.py --reset    # restore the seed data first
python stockpilot.py --fast     # no delays (development only)
```

## Data

`stockpilot.seed.json` holds 46 products in six categories, 7 of them at or below their
reorder level, and no stock movements. The app reads and writes `stockpilot.json`, which
`--reset` recreates from the seed.

## What an agent can be asked to do

1. **Look up a product.** Products, type a SKU or name into the search box, double-click
   the row to open its details.
2. **Find products.** Search by part of a name or SKU; results may span pages.
3. **List low stock.** Tick "Low stock only", optionally pick a category.
4. **Count or rank.** Filter by category, read the "Showing x-y of N" footer, or sort by
   a column header (click again to reverse).
5. **Adjust stock.** Open a product, "Adjust stock...", enter a change, pick a reason,
   Save. A change that would take stock below zero is refused with a message.
6. **Create a product.** "+ New product", fill the form, Create. A duplicate SKU is
   rejected inside the form.

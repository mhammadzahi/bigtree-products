# Vendor Quotation Template — Structure Reference

This document describes the layout, fields, and calculation logic of the company's
Google Sheets "Quotation" template, so it can be used to build a
**"Generate PDF Quotation"** feature: staff pick a product in the internal
product catalog (staff.bigtree-group.com), click a button, and the webapp
fills a copy of this template via the Google Sheets API and exports it to PDF.

The source file has (at least) two sheets with an identical layout:
- **Quote** — the live/current quotation
- **Copy of Quote** — a duplicate of the same layout, apparently used as a
  draft/what-if version. It has stale figures and several `#REF!` errors,
  meaning some of its formula cells reference ranges that no longer exist.
  Treat any formulas taken from the sheet as a *guide*, not something to port
  verbatim — some are broken and should be rebuilt cleanly.

> Note: the values below describe the **shape** of the template only.
> Exact row/column (A1) addresses aren't captured in a CSV export, so before
> wiring up the Sheets API, open the real spreadsheet and note the actual
> cell references for each field named below.

---

## 1. Header block

| Field | Notes |
|---|---|
| Document title | Static text, e.g. "QUOTATION" |
| Date | Date the quotation is issued |
| Quotation No. | Format looks like `<VENDOR CODE> - <YEAR><SEQUENCE>`, e.g. `XXXX - 2026300403` |

## 2. Vendor / Buyer info block (two columns side by side)

**Vendor (left column, static — belongs to the company issuing the quote):**
- Vendor / company name
- Address (2 lines)
- Tel
- Mobile
- Email

**Buyer (right column, variable per quotation):**
- Buyer / company name
- Contact (person name)
- Project (project name)
- Mob
- Tel
- Fax
- Email

## 3. Line items table

**Customer-facing columns** (these are what should appear on the exported PDF):
- Item No.
- Description
- Product Image
- Quantity
- Unit
- Unit Price
- Total Amount (in AED) — `= Quantity × Unit Price`

**Internal cost-breakdown columns** (used to derive margin/selling price —
likely hidden or on an internal-only view, not meant for the customer-facing PDF):
- Price (EUR)
- Price (AED)
- Discount
- Total Cost
- Freight
- Customs
- Total Landed
- Warehousing
- Domestic Transport
- Distribution
- Total Landed Cost
- Profit
- Unit Price (selling)
- Other Project Expenses
- Total PE
- Total Selling Unit Price (AED)

**Rate/percentage row**, positioned right under the column headers, holds the
multipliers applied to the columns above for that line item:
EUR→AED exchange rate, Discount %, Freight %, Customs %, Warehousing %,
Domestic Transport %, Distribution %, Profit %, Other-Project-Expenses %.

**Sub-description rows**: each item's row can be followed by extra free-text
rows describing that item in more detail (e.g. a size/quantity note, material
origin, thickness/spec, finish/surface). These carry no numeric data of their
own — they're just additional description lines tied to the item above them.

### Inferred calculation chain (rebuild rather than copy — some source
formulas are broken):
1. `Price (AED) = Price (EUR) × exchange rate`
2. `Total Cost = Price (AED) × (1 − Discount%)`
3. `Total Landed = Total Cost + (Total Cost × Freight%) + (Total Cost × Customs%)`
4. `Total Landed Cost = Total Landed + Warehousing% + Domestic Transport% + Distribution%` (applied on relevant bases)
5. `Unit Price (selling) = Total Landed Cost × (1 + Profit%)`
6. `Total Selling Unit Price (AED) = Unit Price (selling) + allocated Other Project Expenses`

## 4. Ex-Works subtotal

- **TOTAL EX-WORKS (AED)** row: numeric total **and** the amount spelled out
  in words (e.g. "ONE HUNDRED SIX THOUSAND FIVE HUNDRED AND SEVENTY-FIVE AED
  ONLY"), alongside the internal cost roll-up totals for that column set.

## 5. Logistics / charges section

A fixed list of charge line-items, each with a **status** (`INCLUDED` /
`NOT APPLICABLE`), an **AED amount**, and a **%** of the base cost:

- Freight Ex-Works (with a routing description, e.g. "AIR FREIGHT TO
  `<DESTINATION COUNTRY>` (DOOR-TO-DOOR)")
- Origin Charges
- Customs (with a description, e.g. "`<COUNTRY>` CUSTOM'S DUTY")
- Export Declaration
- Destination Charges
- Adequate Packing
- Transportation
- Offloading
- Installation
- Distribution
- Freight Insurance

### Side calculator tables (internal use, next to the charges list)
- **Freight rate calculator** (one per leg of the route, e.g. origin→hub and
  hub→destination): Chargeable Weight, Rate, Corona Charges, Elevated Risk
  Charge, Charge per Roll, Charge per Pallet, Fuel Surcharge %, Total,
  Customs %, Total.
- **Dimensional Weight Calculator**, per physical item/package: Length,
  Width, Height, Dimensional Weight, No. of Items, Total Dim. Weight.
  (Standard air-freight volumetric divisor formula — confirm the divisor
  used, e.g. L×W×H / 5000 or /6000.)
- An alternate route line also appears (e.g. "AIR FREIGHT TO `<CITY>`
  (DOOR-TO-DOOR)") — likely a second shipping option shown for comparison.

## 6. Grand total block

- **TOTAL DELIVERED IN `<DESTINATION>`** — amount in words + AED figures
- **VAT ON TAXABLE AMOUNT (`<DESTINATION>` BORDER)** — amount + %
- **GRAND TOTAL AMOUNT (AED)**

## 7. Terms & Conditions (static boilerplate — safe to hardcode)

A fixed numbered list, e.g.:
1. Price basis (currency + Ex-Works)
2. Payment terms (e.g. 100% advance)
3. Lead time
4. Delivery terms (single destination only; vertical delivery quoted separately)
5. Payment/bank details block: Bank, Account Name, Account No., IBAN, SWIFT
6. Note that the quotation is subject to change if items/specs/quantities change
7. Validity period (e.g. 60 days) and that it doesn't reserve stock
8. Delivery acceptance window + daily warehousing penalty for late pickup
9. Warranty terms
10. Freight insurance inclusion note

## 8. Footer

- Date field
- "Company Stamp & Signature" field (left blank for physical/digital signing)

---

## What varies per quotation vs. what's fixed

| Fixed (template boilerplate) | Variable (filled per quotation) |
|---|---|
| Title, column headers, T&Cs text, bank details, vendor block | Date, Quotation No. |
| Charge line labels (Freight, Customs, etc.) | Buyer block (company, contact, project, phone/fax/email) |
| Formula logic | Line items (description, image, qty, unit, unit price) |
| | Sub-description rows per item |
| | Logistics charge status/amount/% per shipment |
| | Grand totals (computed) |

## Product data available from the catalog (staff.bigtree-group.com)

Based on the internal product tool, each product record exposes at least:
- ID, SKU, Slug, Type, Stock status
- Name/title
- Product image
- Taxonomies & attributes (e.g. Brand, Category, Colour, Composition, Application)

These map naturally onto the line-item's Description / Product Image columns
when a staff member picks a product to quote.

## Suggested integration approach

1. **Copy the template** per new quotation using the Drive API
   (`files.copy`) or Sheets API (`spreadsheets.sheets.copyTo`) rather than
   editing the master template in place.
2. **Populate fixed-position cells** via `spreadsheets.values.update`/
   `batchUpdate`, using the field map above — confirm real A1 ranges by
   inspecting the actual sheet first.
3. Let the sheet's formulas recompute totals (or reimplement the calculation
   chain in the backend if the sheet's own formulas are unreliable — see the
   `#REF!` errors in "Copy of Quote").
4. **Export to PDF**: either the Sheets export endpoint
   (`https://docs.google.com/spreadsheets/d/<id>/export?format=pdf&...`
   with print-range params) or the Drive API `files.export` with
   `mimeType: application/pdf`.
5. Return the generated PDF to the staff user from the "Generate PDF
   Quotation" button, and optionally store the copied Sheet ID against the
   quotation record for later edits.

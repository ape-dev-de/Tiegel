---
id: p1
title: "Add a discount"
status: drafting
phase: draft
created: 2026-11-27T10:00:00Z
updated: 2026-11-27T10:00:00Z
revision: 0
---

# Add a discount

## Goal {#goal}

Add a discount. It must apply to totals over 100.

## Change requests {#comments}

- Also negative totals (2026-11-27 10:00)

## Context {#context}

Carts compute totals.

References: src/Cart.php

## Findings {#findings}

- **F1** [verified] Totals are in `Cart::total` — `src/Cart.php:10` _(seen)_

## Plan {#plan}

- [ ] **S1** Add Discount (`src/Discount.php`)
  new class
  _Note: n_
- [ ] **S2** Wire it — partial

## Test plan {#tests}

- **T1** discount over 100 [new] — passed
  Given a cart of 120; when total(); then 108.
  File: `tests/CartTest.php` · tests: CartTest::testDiscount

## Acceptance criteria {#criteria}

- [x] **AC1** 10% off over 100 ← T1
- [ ] **AC2** unmapped ← (no test)

## Risks and open questions {#risks}

- **R1** (risk) rounding

## Review log {#review}

- **Q1** [major/open] critic → dev (impl): rounding is off — `src/Discount.php:5`
  Suggestion: round half up
  - dev: **reject** it is fine — `src/Discount.php:6`
  - decided by mediator: qa is right. spec says half up

## Decisions {#decisions}

- **D1** (mediator) Q1: qa is right

## Gates {#gates}

- **tests** passed — 1 tests
- **red** skipped: in place
- **hollow** FAILED

## Summary {#summary}

Adds a discount.

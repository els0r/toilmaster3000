# What a context switch costs an average Zurich developer

*Research date: 2026-06-25*

## Headline

- **One meaningful, flow-breaking context switch ≈ CHF 15** (defensible band CHF 10–25).
- **One trivial micro-switch** (a copy-paste, a tool toggle, a glance at a notification) **≈ CHF 1**.
- The real damage is **frequency**: **≈ CHF 100/developer/day, ~CHF 23,000/year** — roughly **one-fifth of a loaded salary** lost to refocus time.

A "context switch" here means any meaningful action that breaks flow state: copy-paste from tool A to B, switching from coding to Slack, switching tools.

## Input 1 — Fully-loaded cost of a developer-minute in Zurich

| Item | Value | Source |
|---|---|---|
| Avg / median dev salary, Zurich | CHF 111k avg, 112.5k median | [SwissDevJobs](https://swissdevjobs.ch/salaries/all/Zurich/all) |
| Software engineer avg (broader) | CHF 121k | [levels.fyi](https://www.levels.fyi/t/software-engineer/locations/greater-zurich-area), [Glassdoor](https://www.glassdoor.com/Salaries/zurich-switzerland-software-engineer-salary-SRCH_IL.0,18_IM1144_KO19,36.htm) |
| Base used | **CHF 115,000** | midpoint |
| Employer on-costs (AHV/IV/EO, ALV, BVG, UVG…) | **≈ +14%** | [Findea](https://www.findea.ch/en/faq-fiduciary/social-security-contributions-switzerland-employers), [Hanna Treuhand](https://www.hanna-treuhand.ch/en/social-insurance-contribution-update-for-2025/) |
| **Loaded employer cost** | **≈ CHF 130,000/yr** | |

Paid working hours: 42 h/week × 52 = 2,184, minus ~34 days vacation + public holidays ≈ **1,900 h/yr**.

```
Loaded cost per hour   = 130,000 / 1,900 ≈ CHF 68/h
Loaded cost per minute ≈ CHF 1.15/min      (gross-salary-only basis ≈ CHF 1.00/min)
```

## Input 2 — How much time a switch actually destroys (the research)

**A genuine flow break** (coding → Slack thread → back; a deep tool switch):

- Gloria Mark (UC Irvine, *CHI 2005/2008*): **23 min 15 s** to return to the interrupted task — but that elapsed time includes ~2.26 intervening tasks, so it is an upper bound, not pure recovery.
- Parnin & Rugaber, *Programmer Interrupted* (2010): a developer needs **10–15 min just to resume editing code**, and resumes in under a minute only ~10% of the time.
- Developer-specific recovery → **10–23 min**, midpoint **~15 min**.

**A micro-switch** (copy-paste A→B, glance at a notification, tool toggle): no full flow rebuild, but "attention residue" still degrades the next task — call it **30 s–2 min**.

## The arithmetic

| Switch type | Time lost | × CHF 1.15/min | Cost |
|---|---|---|---|
| Micro-switch (copy-paste, toggle) | 0.5–2 min | | **~CHF 1** |
| **Meaningful flow break** (coding↔Slack, deep tool switch) | 10–23 min | | **CHF 11–26 → ~CHF 15** |

**Sanity check via the daily aggregate.** Industry studies converge on **1–2 hours/day lost** to switching for developers ([Jellyfish](https://jellyfish.co/library/developer-productivity/context-switching/), [ShiftMag / GitHub data](https://shiftmag.dev/do-not-interrupt-developers-study-says-5715/)). At CHF 68/h that is **CHF 70–135/day → ~CHF 16k–31k/year**, headline **~CHF 100/day, ~CHF 23k/yr**. This lands deliberately below the often-cited "$50k/yr" figure, which assumes a US loaded cost and aggressive recovery times; CHF 23k is the conservative, defensible Zurich version.

The aggregate also reconciles the per-switch number: ~1.5 h/day spread over ~10–20 genuine flow breaks ≈ 5–9 effective minutes each, which is why the headline is anchored at **~CHF 15** rather than the naïve "23 min × every toggle" (that math would exceed a full working day).

## Caveats

- Recovery-time studies are general knowledge-worker / developer research (Mark, Parnin), **not Zurich-specific**.
- Salary figures are self-reported aggregates.
- A "switch" is a spectrum, so any single number is an average over a wide distribution — read the per-switch figure and the daily aggregate together.

## Sources

- [SwissDevJobs — Zurich developer salaries](https://swissdevjobs.ch/salaries/all/Zurich/all)
- [levels.fyi — Software Engineer, Greater Zurich](https://www.levels.fyi/t/software-engineer/locations/greater-zurich-area)
- [Glassdoor — Software Engineer Zurich](https://www.glassdoor.com/Salaries/zurich-switzerland-software-engineer-salary-SRCH_IL.0,18_IM1144_KO19,36.htm)
- [Findea — employer social contributions Switzerland](https://www.findea.ch/en/faq-fiduciary/social-security-contributions-switzerland-employers)
- [Hanna Treuhand — 2025 social insurance contributions](https://www.hanna-treuhand.ch/en/social-insurance-contribution-update-for-2025/)
- [Gloria Mark / 23-minute refocus research overview](https://gitscrum.com/en/solutions/pains/23-minutes-to-refocus-after-each-interruption-context-switching)
- [Programmer Interrupted (Parnin) — summary](https://contextkeeper.io/blog/the-real-cost-of-an-interruption-and-context-switching/)
- [Jellyfish — context switching](https://jellyfish.co/library/developer-productivity/context-switching/)
- [ShiftMag — cost of interrupting developers / GitHub data](https://shiftmag.dev/do-not-interrupt-developers-study-says-5715/)

**PRD — calbot: a food logging API**

**Goal**
A universal HTTP API for logging food, with a Ukrainian-first food database and
smart search. Clients build their own flows on top of it; the API ships none.

**Why it exists**
FatSecret's search is US-centric, so Ukrainian products and home dishes mostly
do not resolve. Worse, logging something it does not know requires finding a
similar entry in its database rather than typing the numbers on the packet.
Both of those are database and API problems, and both are fixable here.

**Non-goals**
Clients of any kind. UI, copy, notification scheduling, meal-type grouping,
exercise, water, social features. Anything a caller can compute from data the
API already returns.

---

**Design rules**

*Logging never requires a lookup.* An entry may carry its own nutrition values
and a name. No food record, no search, no prior art. The food database makes
repeat logging fast; it is never a precondition for logging at all.

*The API returns data, not views.* No endpoint welds together entries, totals
and targets because one client wanted that screen. A day is a date range. A
"repeat yesterday" is a range read followed by writes.

*Reads have no side effects.* Search does not write analytics rows or queue
anything. A caller that wants an event recorded sends one.

*Product opinions live in clients.* Rolling averages, confidence bands, how
many recent foods to show, whether to render going over target differently:
all caller decisions, served by returning the underlying numbers.

*One credential scheme.* A bearer token, whatever issued it.

---

**Food database**

This is the core deliverable and the part no client can replace.

| Type | Keyed by | Values |
|---|---|---|
| Dish or raw food | name + aliases | per 100 g, named portions in grams |
| Packaged product | barcode / name | per 100 g, package size |
| User food | owner + name | per 100 g, created from values alone |

Every entry records which preparation its values describe when that changes the
number, carries aliases in Ukrainian and Russian including the misspellings
voice transcription produces, and carries provenance: source, licence class and
a confidence rank that decides what may overwrite what.

Coverage today is 323 hand-verified Ukrainian dishes and raw ingredients, 3,122
Ukrainian retail products, and 157 generic reference foods.

**Ingestion**

Adapters run Fetch → Normalize → Validate → Stage → Promote and stop at a
staging table. Promote is the only writer of catalogue rows and refuses to
lower an entry's confidence, so a scraped row cannot overwrite a hand-verified
one. Each source runs isolated: its own run record, its own timeout, and a
failure or panic in one does not stop the rest.

Rejected rows keep their reason and payload rather than vanishing into a log.

---

**Smart search**

One endpoint, several retrievers, chosen by what the query looks like.

*Exact* on barcode or normalized alias returns immediately and ranks nothing.

*Lexical* uses trigram similarity over an alias table whose keys are folded for
the letter pairs Ukrainian and Russian transcription confuses, so misspellings
and cross-spelling variants still match.

*Semantic* is a later addition for cross-lingual and descriptive queries, where
a caller asks for something the catalogue names in another language or does not
name at all. Deferred until an agent client exists to need it.

Results fuse by rank rather than by score, because the retrievers produce
numbers on scales that cannot be compared. Ownership and licence filters apply
before fusion.

A separate parse call takes a free-form phrase, extracts an amount from it, and
returns candidates. That is a convenience for callers that want it, not the
path logging has to take.

---

**Acceptance criteria**

- Logging a food the database has never seen takes one request.
- A search that matches nothing returns an empty list, not an error.
- Search is safe to call repeatedly and changes no state.
- A caller holding only a bearer token can exercise every route.
- No endpoint returns a shape assembled for one screen.
- Catalogue freshness per source is observable, and a stale source is visible
  without reading logs.

---

**Build order**

1. Identity: accounts and bearer tokens, issued however a caller can get one.
2. Entries that carry their own nutrition.
3. Foods as a plain resource: create from values, search split from parse,
   stable portion IDs, promote an entry into a food.
4. Remove the view-model endpoints the first client needed.
5. Composite foods: ingredients plus yield, values computed on write.
6. Semantic retrieval, once something needs it.

The open decisions, with the trade-offs written out, are in
`.claude/api-redesign.html`

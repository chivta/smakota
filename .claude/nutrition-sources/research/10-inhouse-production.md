# In-house production nutrition (кулінарія, hot counter, own bakery)

Verified live on 2026-10-19 with `curl -sL -A "Mozilla/5.0 ..."`, `jq` and python regex over fetched HTML. No search snippet is used as evidence. Scope: which Ukrainian chains publish nutrition for food they cook or bake themselves, as opposed to labelled packaged goods. Сільпо (confirmed by the requester, not re-checked) and Фора are excluded from the work below. The Фора dead-end entry in `deadends.json` is overturned: fora.ua prerenders a `schema.org/Product` JSON-LD block to crawler user-agents (see "Crawler user-agent recheck"); `deadends.json` and `sources.json` were not edited by that pass. Sources already catalogued in `sources.json` are cited, not re-derived.

Limits: counts are from category listings of one store per chain, so they are per-store numbers, not chain totals. "In-house" is inferred, not declared by any API: zakaz.ua marks internal-code items with an `ean` that starts with the chain slug (for example `novus02855969000000`, `auchan02529657000000`) instead of a real barcode, with `unit: kg` for counter items. That set also includes weighed packaged items such as loose bread, so treat "internal-EAN items" as an upper bound and "kg items in the Кулінарія category" as the tighter figure. Nutrition values in `nutrition_facts` carry no stated basis; they read as per 100 g (Олів'є 232.7 kcal, 21.3 g fat) but that is inferred. Own websites were fetched only as far as one category and one to three product pages; that is stated per row.

## The finding

zakaz.ua (stores-api.zakaz.ua) publishes nutrition for the in-house kitchens of Novus, Auchan, Таврія В, Космос, Megamarket and ЕКО маркет, in the same `nutrition_facts` object the packaged goods use. Each of those chains has a Кулінарія category (`ready-meals*`) and a Пекарня category (`bakery*`). Novus's own site novus.online now 301-redirects to novus.zakaz.ua (`utm_campaign=sunset`), so zakaz.ua is the only Novus catalogue. Nobody else surfaced in-house nutrition on the first pass: ATB, METRO, Наш Край / SPAR, Рукавичка, Велмарт and Megamarket's own site all showed either no nutrition for in-house items or no in-house line. The crawler recheck below overturns this for Varus: its weighed in-store bakery (`/vlasna-vipichka`) publishes real per-100 g nutrition on its own site.

## Endpoint (zakaz.ua)

```
GET https://stores-api.zakaz.ua/stores/{store_id}/categories/{category_id}/products/?page={n}
Accept-Language: uk
```

No key. `page` is 1-based, 30 items per page, loop until `results` is empty. Store ids come from `GET /stores/` (field `retail_chain` gives the chain slug). `GET /stores/{id}/categories/` lists category ids. Free-text search also works: `/stores/{id}/products/search/?q=<urlencoded>`. Response: `results[]`, each with `ean`, `sku`, `title`, `unit` (`kg` or `pcs`), `price` (kopecks), `is_ready_meal` (bool), `ingredients[]`, `producer.trademark`, `web_url`, and `nutrition_facts`:

```
"nutrition_facts": {"ingredient_energy":"232.70ккал","ingredient_protein":"5.90г","ingredient_fat":"21.30г","ingredient_carbohydrates":"4.40г"}
```

Values are strings with the unit glued on, comma-free. Items without data return `{}` or omit keys (some have no carbohydrates). Energy is kcal only, no kJ. Category ids used:

| Chain | Store id used | Кулінарія category | Пекарня category |
|---|---|---|---|
| Novus | 482010105 | `ready-meals` | `bakery` |
| Auchan | 48246401 | `ready-meals-auchan` | `bakery-auchan` |
| Таврія В | 482211449 | `ready-meals-tavriav` | `bakery-tavriav` |
| Космос | 48225531 | `ready-meals-cosmos` | `bakery-cosmos` |
| Megamarket | 48267601 | `ready-meals-megamarket` | `bakery-megamarket` |
| ЕКО маркет | 482800030 | `ready-meals-ekomarket` | `bakery-ekomarket` |
| METRO | 48215610 | none (no Кулінарія category) | `bakery-metro` |

## Chains

Counts: "Кулінарія items" is the number of products returned by the Кулінарія category for the single store listed above; "with kcal" is how many of those have a non-empty `ingredient_energy`. "Internal-EAN items" is the same count over Кулінарія plus Пекарня, deduplicated by `ean`, restricted to chain-prefixed EANs (upper bound for in-house).

| Chain | In-house line | Nutrition published? | Where | Proof item (verified live) | Approx items | Terms |
|---|---|---|---|---|---|---|
| Novus | Кулінарія and Пекарня, brand "Майстри Смаку" (their kitchen, 246 of 250 ready-meal items) | Yes, zakaz.ua only (novus.online redirects there) | `stores/482010105/categories/ready-meals/products/` | Салат Олів'є (`novus02855969000000`, 299 UAH/kg): 232.70 kcal, 5.90 g protein, 21.30 g fat, 4.40 g carb. Also Шашлик Барбекю свинячий 323 kcal; Піца Маргарита 500г 196.9 kcal / 9.4 / 7.6 / 24.4 | Кулінарія 290 items, 155 with kcal (125 flagged ready-meal with kcal); internal-EAN 434, 267 with kcal, 259 with all four values | zakaz.ua terms forbid reuse, see below |
| Auchan | Кулінарія and Пекарня, brand "Auchan" (110 Auchan-brand kg/pcs ready meals) plus third-party packaged | Yes, zakaz.ua | `stores/48246401/categories/ready-meals-auchan/products/` | Шашлик Auchan з цибулею (`auchan02529657000000`, 229.90 UAH/kg): 246.80 kcal, 11.60 / 22.00 / 1.12. Пиріжки смажені з капустою: 189.69 kcal, 5.7 / 5.6 / 31.8 | Кулінарія 380 items, 338 with kcal; internal-EAN 461, 403 with kcal, 382 with all four | same |
| Таврія В | Кулінарія (no trademark, kg weighed; 194 of 253 ready meals are "без тм" kg) and Пекарня "Наш Хліб" | Yes but patchy | `stores/482211449/categories/ready-meals-tavriav/products/` | Лаваш з сиром сулугуні (`tavriav02000000243368`): 212.44 kcal, 10.75 / 9.33 / 21.42. Шашлик курячий Маршал: 168 kcal, 16.5 protein, 11 fat, no carb key | Кулінарія 262, 149 with kcal; internal-EAN 383, 169 with kcal, 145 with all four | same |
| Космос | Кулінарія (127 of 142 ready meals "без тм" kg) and Пекарня "Наш Хліб" | Yes | `stores/48225531/categories/ready-meals-cosmos/products/` | Риба Мойва смажена (`cosmos02000000370262`): 206.10 kcal, 13.84 / 15.08 / 3.32 | Кулінарія 153, 99 with kcal; internal-EAN 240, 150 with kcal, 131 with all four | same |
| Megamarket | Кулінарія and Пекарня; counter items "без тм" kg plus brand "The Local Food" (packed lunch boxes, 105 items) | Yes, highest fill rate | `stores/48267601/categories/ready-meals-megamarket/products/` | Млинці смажені (`megamarket00000000145244`): 259.46 kcal, 5.18 / 14.81 / 26.40. Ланч бокс The Local Food №74 430г: 111.48 kcal, 6.5 / 2.27 / 16.27 | Кулінарія 280, 272 with kcal; internal-EAN 337, 325 with kcal, 323 with all four | same |
| ЕКО маркет | Кулінарія, 28 weighed counter items plus 14 pcs | Yes | `stores/482800030/categories/ready-meals-ekomarket/products/` | Курка гриль (`ekomarket00000000530577`): 193 kcal, 25.0 / 10.0 / 1.6. Четвертина куряча гриль: 182 kcal, 17.5 / 12.53 / 1.6 | Кулінарія 42, 28 with kcal; internal-EAN 62, 43 with kcal | same |
| METRO | None found. The Metro store has no Кулінарія category; searches for салат and шашлик return only Metro Chef packaged lines, spice mixes and third-party chilled meat (Наша Ряба). Only 12 internal-EAN items, all packaged bakery (Metro Chef, Fine Life). metro.ua itself returns 403 to curl | Packaged only: e.g. Тарталетки Metro Chef Фуршетні 288г 488 kcal, 7.8 / 25.8 / 54.5 | n/a | n/a | 0 in-house | same |
| ATB | Yes by name: catalog `/catalog/502-kulinariya` (Кулінарія) and `/catalog/482-garyacha-vipichka` (Гаряча випічка, 36 items on page 1). Items include Чебурек 150г з курячим м'ясом, Бypгеp 165г Koh Grills, Багет міні Festory. www.atbmarket.com is NOT Cloudflare-blocked today (HTTP 200 with a desktop UA). | No. Product pages carry a "Харчові властивості" block (Калорійність, Вуглеводи, Білки, Жири) but it sits inside an HTML comment and holds the same placeholder on every product (1791 кДж/100г, 88.1 г/100г carbs, 2 г/100г protein, seen identically on 3 different items). No rendered nutrition on those 3 in-house pages, on one bread page, or on the meat page checked. `/api/v1/products` returns the HTML shell, not JSON | n/a | n/a | Кулінарія page lists 6 products on page 1, Гаряча випічка 36 | No licence found |
| Varus | Partly. "Готові страви" (`/gotovi-stravi`, 34 items) are third-party packaged (М'ясторія, Urban Eats, Легко, Giovanni Rana, Risumi). `/vlasna-vipichka` (40 products on the first listing page, all "вагова") is the in-store bakery: Багет пшеничний, Пампушка з часником, Сосиска у тісті, Самса з курячим м'ясом, Булочка Варус. "Власне виробництво" is a brand filter, not a counter | Yes, real values for in-house bakery weighed items and for packaged ready meals (see recheck): Багет пшеничний ваговий 224.3 ккал, 7 / 1.54 / 45.6; Самса з курячим м'ясом вагова 271 ккал, 9.1 / 15.7 / 23.3. Some pages have no keys (Хліб Тігріс, Борщ Urban Eats). Earlier claim that pizza "BR" was a brand is wrong: `"brand":"BR"` is an analytics key map, not a brand | Product page HTML, keys `energyKcal`, `protein`, `fat`, `carb` (strings like `224.3 ккал/100 г`) in the embedded state JSON; not in JSON-LD. The listing pages also embed their first-page products in the HTML (name, sku, `url_key`), so they are not purely client-rendered | Same shape as `08-gaps-retail.md` | 40 bakery items on page 1 of `/vlasna-vipichka` (listing says 640 overall); 34 ready meals | No licence found |
| Наш Край | Yes: `/lutsk/category/kulinariya`, 8 pages of 12, about 100 weighed items ("ваг"): Курка Гриль, Плов зі свининою, Чебуреки з мясом ФК, Жульєн | No. Three product pages (Курка Гриль ваг, Плов зі свининою ваг, Чебуреки з мясом ФК ваг) show no Калорійність/Білки/Жири/Вуглеводи rows. Packaged items on the same site do show them (track 08) | n/a | n/a | about 100 | No licence found |
| SPAR Україна | `shop.spar.ua` is the same engine and catalogue as Наш Край (same product ids, e.g. `/product/12140-kurka-gril-vag`; the franchisee is the same Volyn operator). Кулінарія at `/lutsk_shop/category/kulinariya`, 12 per page | No. Same three-item result as Наш Край, no nutrition rows (checked Курка Гриль ваг) | n/a | n/a | same as Наш Край | No licence found |
| Рукавичка | Yes but tiny: `market.rukavychka.ua/kulinaria/` shows 4 products, all weighed grill chicken (Курка гриль, Стегна, Гомілки, Четвертинки) | No. Курі бройлери гриль 1кг page has none of ккал, Калор, Білки, Жири, Склад, Харчов | n/a | n/a | 4 | No licence found |
| Велмарт | Yes, strongest in-house brand of the small chains: own pages for Власна пекарня, Випічка з тандиру, Піца меню, Бургер меню, Кулінарія, Graan Cafe food courts | No. The pages are menus with dish names and store addresses, no nutrition (Бургер меню and Випічка з тандиру pages checked, no ккал or Калорій). No online catalogue found | n/a | n/a | not countable | No licence found |
| Копійочка | No in-house line found. Homepage text has no кулінарія or власне виробництво mention; catalogue is packaged only (track 08) | Packaged only (track 08) | n/a | n/a | 0 | No licence found |
| Близенько | No catalogue, store locator only (track 08, dead end) | n/a | n/a | n/a | n/a | n/a |
| Thrash | Homepage is 21 KB with no catalogue, no кулінарія mention found | n/a | n/a | n/a | n/a | n/a |
| Фуршет | `furshet.ua` fails to connect (curl status 000). Chain absorbed by Сільпо-Фуд | n/a | n/a | n/a | n/a | n/a |

## Terms (zakaz.ua)

`https://zakaz.ua/uk/page/terms/` (200 OK) states users must not copy, distribute, sell, or "використовувати будь-яким способом для будь-яких комерційних цілей Сайт та (або) будь-які частини вмісту Сервісу без згоди Zakaz.ua". `https://stores-api.zakaz.ua/robots.txt` returns `User-agent: *` and `Disallow: /`. The API is keyless and open but not licensed for reuse; the same position is recorded in `sources.json` for Zakaz.ua. Per AGENTS.md this is the same posture as other tier-2 sources: `license_class` must be `lookup_only` and never exported.

## Platform notes

- `GET /stores/` returns 67 branches across 21 chains (see `sources.json`). Кулінарія and Пекарня items differ by store, and chain-prefixed EANs encode the item, not the store: a chain-wide pass needs one store per chain at minimum, ideally one per city for kitchens with different menus. Not tested here.
- Novus, Auchan, Megamarket and Космос expose the in-house item as the same item id across the `web_url` host (for example `novus.zakaz.ua/uk/products/...--novus02855969000000/`), so a stable key is the `ean` string.
- `ingredients[]` is populated for counter items (Олів'є lists the full recipe), which is useful for validating nutrition against composition.
- Delivery aggregators: `glovo.com/uk/ua/` and `wolt.com/uk/ukr` both returned 404 for plain curl; no hot-counter nutrition was reachable there. Not pursued further.

## Dead ends

- **ATB nutrition block.** Present in the template, commented out, and filled with an identical placeholder on every product. Do not scrape the commented values: 1791 кДж / 88.1 g carbs / 2 g protein appears on a chicken burger, a chebureck and a baguette. The "ATB shows КБЖУ in Google snippets" claim from track 08 was not reproducible on 2026-10-19 on the in-house or bread pages checked. Packaged items not rechecked.
- **METRO in-house.** No Кулінарія category on any store; only Metro Chef packaged. metro.ua direct returns 403 (Cloudflare-style block, not worked around).
- **Varus hot counter.** There is no hot counter: "Готові страви" are third-party chilled packs and "Власне виробництво" is a brand filter. This does not make Varus a dead end any more: the weighed bakery under `/vlasna-vipichka` is in-house and carries nutrition (recheck below), so this entry is corrected from "Varus in-house" to "Varus hot counter". Listings are server-embedded, not client-rendered, as first claimed; the sitemap route (`varus.ua/media/sitemap/sitemap-2-1.xml`, `-2-2.xml`, 82,467 loc lines combined) still works for full enumeration. One pizza (Прімавера з фетою) and Хліб Тігріс / Борщ Urban Eats have no nutrition keys.
- **Наш Край / SPAR кулінарія.** Real in-house line, zero nutrition on three items. Same catalogue behind two domains, so one finding covers both.
- **Рукавичка.** Four items, no nutrition.
- **Велмарт.** Brochure pages naming dishes, no nutrition, no catalogue.
- **Megamarket own site (megamarket.ua).** Has `Хліб (власна випічка)`, `Піца`, `Салати, соління` categories under server-rendered `/catalog/...` URLs, but the Багет французький and Основа для піци pages contained no ккал, Калор, Білки, Жири. The same kitchen's nutrition is on zakaz.ua. Not further investigated for a JS-loaded block.
- **Таврія В own site (tavriav.ua).** Next.js app on an Instaleap storefront; product pages are `/p/<slug>-<sku>`. Server-rendered identically for every user-agent, and the product state has `nutritionalDetails` and `nutritionalDetailsInformation` fields, but both were empty on the four Кулінар / weighed items checked (recheck below). Its robots.txt returns an S3 AccessDenied XML and `/sitemap.xml` is the SPA shell. Use zakaz.ua.
- **Auchan own site (auchan.ua).** Next.js, confirmed. It is a general hypermarket catalogue under `/ua/` with no Кулінарія category (guessed slugs 404 or 500). Packaged food carries nutrition in the product description text only. Not pursued; zakaz.ua covers it. See recheck below.
- **cosmos.com.ua** redirects to a domain-sale page. Космос is reachable only via zakaz.ua.
- **eko.ua** is the unrelated ТМ Еко spice brand, not ЕКО маркет.
- **Копійочка, Близенько, Thrash, Фуршет.** No in-house line or no working site, as above.
- **Delivery aggregators (Glovo, Wolt).** Plain curl got 404.

## Ranked recommendation

1. **zakaz.ua as one adapter, started with Novus and Auchan.** One endpoint, one JSON shape, no key, and it covers six chains. Novus alone gives about 290 Кулінарія items (155 with kcal) and about 430 chain-coded items with 267 kcal values in one store; Auchan 380 Кулінарія items, 338 with kcal, 382 with full four values. The numbers look like real specification-sheet values, not placeholders: they vary item to item, have decimals like 232.70 and 189.69, and Olivier lists its recipe in `ingredients[]`. The ranking judges on item count and verifiability, and this is the cleanest source. Terms are restrictive (commercial reuse forbidden, robots.txt `Disallow: /`), so rows must carry `license_class = lookup_only` and never export. Because `sources.json` already lists Zakaz.ua for packaged goods, this is an extension of that adapter (add Кулінарія/Пекарня categories and `is_ready_meal`), not a new package.
2. **Megamarket, within the same zakaz.ua adapter, as a priority store.** Highest completeness: 272 of 280 Кулінарія items carry energy and 323 of 337 chain-coded items carry all four values. Cheap to include once the adapter exists.
3. **Таврія В and Космос, also within the same adapter, with a completeness filter.** Fill rate is 44 to 66 percent and some rows lack carbohydrates, so the Validate stage should reject rows missing any of the four values rather than store partial macros. ЕКО маркет is small (28 weighed counter items) and can ride along.

Not worth an adapter now: ATB (nutrition not rendered, placeholder values, crawler agents blocked), METRO, Наш Край and SPAR (real kitchen, no nutrition), Рукавичка, Велмарт. Varus moved out of this list by the crawler recheck: its own-site bakery is a candidate second source after zakaz.ua (about 40 weighed items with nutrition, product pages fetchable with a plain user-agent; the 640-item figure is unverified).

Open point: whether the same dish differs across stores of one chain on zakaz.ua was not tested; the figures above are one store per chain.

## Crawler user-agent recheck

Done 2026-10-19. Premise from the brief: fora.ua serves a 7,520-byte SPA shell to a browser user-agent and a prerendered page (296,494 bytes today for `/product/banan-32485`, was 298,835 for another item) with a `schema.org/Product` JSON-LD block to `Googlebot/2.1` and `bingbot/2.0`. Re-confirmed here: browser 7,520, Googlebot 296,494, bingbot 296,494, JSON-LD present only for the crawler agents. Question: does any other chain do the same?

Method: each product page was fetched with the three agents from the brief (Chrome 131 on Linux, `Googlebot/2.1`, `bingbot/2.0`); robots.txt was fetched first with `calbot/1.0`. The crawler agents were not used against any path a robots.txt disallows, and not against METRO or Фуршет (see below).

Result in one line: no chain other than Фора prerenders differently for crawlers. Every other site is either server-rendered identically for all agents (Varus, Наш Край, SPAR, Megamarket, Таврія В, Auchan, Рукавичка) or fronts crawler-agent requests with a Cloudflare challenge (ATB, plus bingbot on Auchan and Рукавичка). The only new data this opens is on Varus.

### robots.txt

| Site | robots.txt | Product and catalogue paths |
|---|---|---|
| www.atbmarket.com | 200 | `Allow: /catalog/*`, `/product/*`; disallows `*search*`, `sort=`, `per-page=`, `/all-goods*`, `/feed*` |
| varus.ua | 200, no Disallow lines | permitted |
| shop.nashkraj.ua, shop.spar.ua | 200 | disallow cabinet, login, `/search/`, `/cart/`, `?filter=*`, `sort=`; category and product permitted |
| megamarket.ua | 200 | `Googlebot` and `AdsBot-Google` fully allowed; `*` disallows `/api/`, `/cart/`, `/order/`, `/user/` and similar; `/catalog/` and `/products/` permitted; `Crawl-delay: 3`; BLEXBot and Yandex fully disallowed |
| tavriav.ua | 403 S3 AccessDenied (no file) | no stated restrictions |
| auchan.ua | 200 | disallows `/catalogsearch/`, `/?`, `/checkout/`, `/account/`; `Allow: /p=`; product pages under `/ua/<slug>/` are not disallowed |
| market.rukavychka.ua | 200 | disallows search, cart, checkout, account, `sort=`, `limit=`; product and `/kulinaria/` permitted |
| www.metro.ua | 403 "ARE YOU LOST?" block page, also for robots.txt | unreadable, so no crawler agent was tried |
| furshet.ua | no connection: TLS certificate does not match the host, and `curl -k` also returns nothing | not fetched |
| fora.ua | 200 | `/product/` permitted |

### Table

Sizes are bytes for the browser / Googlebot / bingbot responses on the same product page. Prerenders means the crawler gets materially different content than a browser.

| Chain | Sizes (browser / Google / bing) | Prerenders for crawlers | Nutrition now reachable | Earlier verdict |
|---|---|---|---|---|
| Фора (fora.ua) | 7,520 / 296,494 / 296,494 | Yes, JSON-LD `Product`, `description` field | Yes per the brief (egg values); the banana page checked has none (fresh produce) | Dead end overturned |
| ATB | 550,202 / 5,638 (403) / 5,702 (403) | No. Browser page is server-rendered; crawler agents get a Cloudflare "Just a moment" challenge | No. Block still commented out with the same 1791 кДж / 88.1 / 2 placeholder; Product JSON-LD has no nutrition | Stands; extra note that the crawler route is closed |
| Varus | 1,617,817 / 1,167,507 / 1,167,507 (salad); 1,620,984 / 1,170,672 / 1,170,672 (ketchup) | No. All three agents get the same nutrition keys; the crawler page is about 450 KB smaller (extra inline state), not a shell | Yes, real values, for packaged and for in-house bakery | Overturned in part: bakery is in-house with nutrition; "BR" misread; listings are not client-rendered |
| Наш Край | 58,446 / 58,446 / 58,446 (Курка Гриль ваг); 67,726 / 67,757 / 67,731; 67,622 / 67,615 / 67,673 | No, server-rendered, near identical | No | Stands |
| SPAR | 56,420 / 56,420 / 56,420 | No | No | Stands |
| Megamarket | 204,337 / 204,338 / 204,338 (Багет Megamarket); 205,838 x3 (hummus) | No | No, not even on a packaged control | Stands |
| Таврія В | 795,081 / 795,027 / 795,138 | No, Next.js SSR for all agents | No on 4 items checked; fields exist but are empty | Stands (earlier "not drilled" now drilled) |
| Auchan | 740,875 / 740,875 / 5,777 (403) on home; 702,778 / 702,735 / 5,776 (403) on a product | No; browser and Googlebot identical; bingbot challenged | No in-house line on this site; packaged goods have nutrition in description text only | Stands; earlier "not drilled" now drilled |
| Рукавичка | 130,041 / 130,041 / 12,588 (403) | No; bingbot challenged | No (Курі бройлери гриль, Четвертинки: no ккал, Білки, Жири, Харчов) | Stands |
| METRO | 403 for every request including robots.txt | Unknown | Not tested: a 403 block page is not worked around by spoofing | Stands (zakaz.ua only) |
| Фуршет | no connection | n/a | n/a | Stands, reason refined: TLS certificate mismatch, not a plain timeout |

### Findings per chain

**ATB.** robots.txt allows `/product/*` and `/catalog/*`, so Googlebot would be permitted on the pages tested. It makes no difference: the three Кулінарія and Гаряча випічка pages (Багет міні Festory, Бypгеp 165г Koh Grills, Чебурек 150г з курячим м'ясом) answer both crawler agents with a 5.6 KB Cloudflare challenge ("Just a moment...", `noindex,nofollow`). The browser page is 545 to 550 KB, carries a `BreadcrumbList` and a `Product` JSON-LD that has name, price (42.90 UAH), sku and brand but no nutrition, and the same HTML-commented "Харчові властивості" block with 1791 кДж/100г, 88.1 г/100г carbs, 2 г/100г protein on all three. Real Googlebot probably passes the challenge by IP, but a spoofed agent cannot, and defeating the challenge is out of scope. The "ATB shows КБЖУ in Google snippets" claim stays unreproduced. Verdict stands.

**Varus.** Server-rendered Magento-style storefront; browser and crawler agents get the same nutrition. Nutrition is in the embedded state JSON (`energyKcal`, `protein`, `fat`, `carb`, each a string like `"224.3 ккал\u002F100 г"`), not JSON-LD (the `Product` JSON-LD has an empty `description`). Values are real and vary per item, so not placeholders. In-house bakery (`/vlasna-vipichka`, all items weighed "вагова"):

| Item | kcal | protein | fat | carb |
|---|---|---|---|---|
| Багет пшеничний ваговий | 224.3 | 7 | 1.54 | 45.6 |
| Пампушка з часником та зеленню вагова | 264 | 6.7 | 9 | 46.57 |
| Сосиска у тісті вагова | 274.3 | 10.8 | 6.6 | 30.4 |
| Самса з курячим м'ясом вагова | 271 | 9.1 | 15.7 | 23.3 |
| Булка здобна Плюшка вагова | 369.9 | 6.9 | 11.32 | 58.9 |
| Булочка Варус вагова | 299 | 7.14 | 9.1 | 45.45 |

Хліб Тігріс пшеничний ваговий has no nutrition keys. Готові страви (third-party brands М'ясторія, Urban Eats, Легко, Risumi, Giovanni Rana, none declared in-house): Салат Легко Олів'є 250 г 200 ккал, 6.1 / 16.5 / 6.9; Плов М'ясторія з куркою та родзинками 188.66 ккал; Котлета Легко куряча 323 ккал; Борщ Urban Eats has none. Whether "Легко" or "Власна випічка" production is Varus's own kitchen is declared nowhere checked, beyond the bakery being weighed in-store; treat as in-house bakery with an inferred basis. Verdict changes: Varus is no longer packaged-only.

**Наш Край and SPAR.** Product URLs in the category listing (`/product/12140-kurka-gril-vag`) 404 without the city segment; the working form is `/lutsk/product/...` on Наш Край and `/lutsk_shop/product/...` on SPAR. Pages are server-rendered with no JSON-LD and no nutrition rows for Курка Гриль ваг, Плов зі свининою ваг and Чебуреки з мясом ФК ваг under any of the three agents; sizes differ by under 100 bytes. Verdict stands.

**Megamarket.** Own site is server-rendered PHP; `/products/<slug>` pages are 204 to 206 KB for every agent, with only Breadcrumb microdata and no nutrition, and the packaged hummus control also shows none. Verdict stands; zakaz.ua remains the only source.

**Таврія В.** The Instaleap storefront renders on the server for every agent (795 KB product pages, 1.98 MB homepage). Product pages are `/p/<slug>-<sku>`, JSON-LD `Product` has name, sku, price and an empty description. State contains `"nutritionalDetails":""` and `"nutritionalDetailsInformation":null` on Картопля запечена з беконом ваг. Кулінар., Печінка смажена з цибулею куряча ваг. Кулінар., Філе гігантського кальмара and a non-food control; the feature flag `nutritionalTable:true` is on, so other items may be populated, but none of the four were. Only four product links are reachable from the homepage, and the sitemap URL returns the app shell, so item enumeration is unsolved. Verdict stands: use zakaz.ua.

**Auchan.** Next.js, not blocked for Googlebot (identical to browser, 702 to 741 KB); bingbot gets the Cloudflare challenge. No Кулінарія category on the `/ua/` site; nutrition appears only as text inside packaged product descriptions (Крупа гречана Auchan Проділ 1 кг: "Калорійність на 100 г продукту: 326 ккал/1370 кДж", protein 9.5%, fat 1.9%). Not a source for in-house food. Verdict stands.

**Рукавичка.** Googlebot gets the same page as the browser; bingbot gets a 12.6 KB 403. Zero nutrition terms on Курі бройлери гриль and Четвертинки куряча гриль. Verdict stands.

**METRO and Фуршет.** METRO's 403 covers robots.txt too, so robots.txt could not be read and no crawler agent was tried: spoofing past a block page is not what the brief authorises. Фуршет's host resolves (194.247.12.98) but presents a certificate that does not match, and an insecure fetch still returned nothing. Both verdicts stand.

### Verdicts changed by this pass

1. **Фора**: dead end overturned (confirmed the brief's premise; JSON-LD, crawler agents only).
2. **Varus**: "packaged only, no hot counter" corrected to "in-house weighed bakery with real nutrition on `/vlasna-vipichka`"; the "BR brand" reading and "listings client-rendered" claims withdrawn.
3. **Таврія В own site** and **Auchan own site**: "not drilled" replaced by drilled results, both still negative.
4. **Наш Край / SPAR**: no verdict change, but the product URL form that works is now recorded.
5. Unchanged: ATB, Megamarket, Рукавичка, METRO, Фуршет.

Not done: no real Googlebot IP was available, so ATB's challenge for genuine crawler traffic is unknown; Фора's ready-meal items were not enumerated here; `sources.json` and `deadends.json` still list Фора and need updating by someone with scope to edit them.

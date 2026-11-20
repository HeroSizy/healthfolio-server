# People (family profiles): API for the iPhone app

Server version 0.2.0 and later. `GET /health` lists `"people"` in `features` when the server
has this. Older servers do not: then the app sends no `person` and shows no family profiles
for that server.

## Ideas

- A **person** is one human whose health data the server keeps. Each person has their own
  database file. The data of two people is never in the same table.
- A person's **id** is a string the app makes: a random UUID. For the phone owner it is the
  "person id" in iCloud key-value storage (`NSUbiquitousKeyValueStore`), so the same Apple ID
  on another iPhone or iPad gets the same id. When iCloud is off, the app uses its device id.
  For a family member without a phone, the app makes a UUID when it adds the profile.
- The server always has the person `"me"`: the owner of the server. The first phone that
  pairs takes it (its person id becomes an alias of `"me"`). On a server upgraded from an
  older version, all the data that is there belongs to `"me"`, and a phone that was paired
  before takes `"me"` with `POST /people/me` (see below).
- Every paired phone **belongs to one person**. Its token can read and write that person
  only, unless the phone may **manage the family** (`family: true`). Then it can read and
  write every person and add profiles. The owner's phone gets `family: true` when it pairs;
  other phones get `false`. The server owner changes this in the dashboard.
- HealthKit data is always the phone owner's: the app never sends HealthKit samples with
  another person. Reports of a family member go with that person.

## Which person a call is for

Every call below that reads or writes data takes the person in one of these ways:

- query parameter `?person=<id>`, or
- header `X-Healthfolio-Person: <id>`.

Without one, the call is for the phone's own person (what older apps do). Answers when the
person is not allowed:

| Status | `reason` | Meaning |
|---|---|---|
| 404 | `unknown_person` | No person with this id. Add it first with `PUT /people/{id}`. |
| 403 | `person_not_allowed` | This phone may not manage the family. Ask the server owner to turn on "Can manage the family" for this phone in the dashboard. |

Calls that take a person: `POST /ingest`, `POST /report`, `GET /stats`,
`POST|GET /backup/chats…`, `GET /backup/reports…`, `POST /mcp/tokens`.
`/backup/settings` is always the phone's own (it is the settings of the phone).

## Pairing

`POST /pair` and `POST /pair/requests` take two more fields:

```json
{"device": "Zen's iPhone", "device_id": "…", "code": "123456",
 "person_id": "6F1C…UUID", "person_name": "Zen"}
```

- `person_id`: the owner's person id (iCloud). Up to 128 characters.
- `person_name`: the name the phone owner chose ("Whose phone is this?"), default from the
  device name. Up to 40 characters. Only a name to show; never an identity.

The server finds the person like this:

1. `person_id` is known: the phone joins that person.
2. The phone paired before (same `device_id`): it stays with its person, and `person_id`
   becomes another id of that person.
3. Nobody has `"me"` yet and no other phone belongs to `"me"`: the phone takes `"me"`.
4. Else: a new person with `person_name`.

An app without `person_id` (older versions) follows the same steps without step 1: it
keeps the person it had, or takes `"me"` while no phone belongs to `"me"`. Any other such
phone becomes a new person named after the device, with `family: false`. Merge it into the
right person in the dashboard if needed.

The answer (from `/pair` and from `GET /pair/requests/{id}` when approved) has two more fields:

```json
{"token": "…", "device": {"id": 3, "name": "Zen's iPhone"},
 "person": {"id": "6F1C…", "name": "Zen", "owner": true},
 "family": true}
```

The pairing dialog on the computer shows the person name too
("Zen's iPhone, for Mum").

## GET /auth

Has `person` and `family` as above. `person` is missing for an AI token that sees everyone.

## POST /people/me

For a phone that paired before the app knew about people. Send once, when `GET /auth` shows a
`person.id` that is not the app's own person id:

```json
{"id": "6F1C…UUID", "name": "Zen"}
```

- 200 `{"person": {…}}`: this id is now (also) the id of the phone's person.
- 409 `{"reason": "person_taken"}`: the id belongs to another person. Only the server owner
  can move the phone (dashboard › Devices › Person).

### Taking over a profile with a hand-over code

A family member's profile was made on a family phone, and then they pair their own phone,
which gets a person of its own. To join the two, the family phone makes a code and the
member's phone sends it:

```json
{"id": "A93E…", "claim": "5d1e…"}
```

- 200 `{"person": {…}}`: the phone's own person is merged into `id` (data, reports with
  the same report id kept once, devices, and its ids, which now also find `id`). The phone
  keeps `family: false`.
- 403 `{"reason": "bad_claim"}`: the code is wrong, used, expired, or for another person.
- 409 `{"reason": "owner"}`: `id` is the server owner, or this phone holds the owner.

A phone that has not paired yet does not need a code: it pairs with `person_id` = that id.

## POST /people/{id}/claims

Family phones only (403 `person_not_allowed` otherwise). Makes a one-time hand-over code for
the person: 200 `{"token": "…", "expires": 1764201600}` (Unix seconds, 7 days). The server
keeps only its hash. 404 `unknown_person`; 409 `owner` for the server owner.

## GET /people

The people this token may see: everyone for a family phone, else only its own person.

```json
{"people": [
  {"id": "6F1C…", "name": "Zen", "owner": true, "me": true,
   "relation": "", "birth": "1990-05-01", "sex": "male",
   "records": 841234, "reports": 12, "last_received": "2026-11-19T21:02:11+08:00", "devices": 2},
  {"id": "A93E…", "name": "Mum", "owner": false, "me": false,
   "relation": "mother", "birth": "1962-03-14", "sex": "female",
   "records": 0, "reports": 4, "last_received": null, "devices": 0}
]}
```

`me` is true for the person this phone belongs to. `owner` is true for the server owner.

## PUT /people/{id}

Adds the profile or changes it. All fields are optional:

```json
{"name": "Mum", "relation": "mother", "birth": "1962-03-14", "sex": "female"}
```

- `relation`: free text from the app (`spouse`, `child`, `mother`, `father`, `other`, …).
- `birth`: `YYYY-MM-DD` or `YYYY`. `sex`: `female`, `male` or empty.
- Adding a person needs a family phone (403 `person_not_allowed` otherwise). A phone may
  change its own person.
- 200 `{"person": {…}, "created": true|false}`.

## DELETE /people/{id}

Family phones only. Deletes the person and **all of their data on this server** (database,
reports, chats). Refused with 409 `person_has_devices` while a phone still belongs to that
person, and with 409 `owner` for `"me"`. 200 `{"ok": true}`.

## AI tokens: POST /mcp/tokens

`{"name": "Claude", "person": "<id>"}`

- A phone that is not a family phone: the token sees its own person only (`person` is ignored
  when it is the phone's own, refused when it is another).
- A family phone: with `person`, the token sees that person only; without it, everyone.

The answer is the same as before. MCP tools then take a `person` argument (see
`list_people`); with one person to see, it can be left out.

## Summary for the app

1. `GET /health` → `features` has `people`?
2. Pairing: send `person_id` + `person_name`. Keep `person` and `family` from the answer.
3. Already paired: `GET /auth`; if `person.id` is not yours, `POST /people/me`.
4. Family profiles: `PUT /people/{id}` when added or changed (`DELETE` when deleted). If 403
   `person_not_allowed`, tell the user to turn on "Can manage the family" for this phone in
   the server's dashboard.
5. Reports of a family member: `POST /report?person=<id>`. HealthKit: no `person`.
6. Reading reports back: `GET /backup/reports` and `/backup/reports/{id}` read the phone's
   own person; a family phone adds `?person=<id>` for someone else (403
   `person_not_allowed` for a plain phone).
7. Hand-over: `POST /people/{id}/claims` on the family phone, then `POST /people/me` with
   `claim` on the member's phone.

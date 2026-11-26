# PAD PROTOCOL — locked spec

Status: **LOCKED**. Every line below was stated by the user. Do not re-derive,
do not reinterpret, do not "improve". If implementation disagrees with this file,
the implementation is wrong.

Any change to this file requires the user to say so explicitly.

---

## 1. Identity

| Field | Rule |
|---|---|
| `client` | UUID held in the page's **`sessionStorage`**. Survives a refresh. One id per browser tab, for the life of the tab. |
| `version` | Integer protocol version, **tied to the software release**. Currently **`1`**. It stays `1` unless the **server code** is modified — then it is bumped. |

The client id is NOT regenerated on page load. A refresh keeps it.

---

## 2. Posting

After every **button release**, the client posts the **event log** — what happened
since its last successful post — not a snapshot of the whole trace.

Every post carries, from the client:

```json
{ "client": "<uuid>", "version": 1, "events": [ ... ] }
```

### Version mismatch

If `version` does not match the server's protocol version, **the client must
refresh** to fetch the new script. That is the only correct response from the
client side; the server refuses the post.

---

## 3. Server

The server's **only** job is to receive the event log.

1. Compare `version` with the server's protocol version. On mismatch, refuse the
   post so the client refreshes. (Refusal, not a silent accept.)
2. **De-duplicate**: the de-duplication key is **`(client, ts)`** — the client id
   and the event timestamp. Nothing else. No epoch, no stroke index, no sequence.
3. **Append** the events that survive de-duplication to a **dedicated append-only
   file**.

The append-only file is itself the de-duplication index. No sidecar set, no
in-memory map that dies with the process: a `(client, ts)` already present in the
file is a duplicate, and is dropped.

---

## 4. The file

- One **append-only** file per client: `strokes.d/<client>.jsonl`.
- One JSON object per line. Appended, never rewritten, never truncated.
- Records are never modified or deleted. The file only grows.

---

## 5. The observer (me)

- The observer reads the **append-only file**. **Only** the file.
- The observer does **not** call any API to learn what was drawn.
- The API exists **solely** to accept the event log from the client.

### Consequence for certification

A drawing is certified from the append-only file, and from nothing else. A run
reported "certified" from a client file read by hand is not certified under this spec.

---

## 6. Non-goals / explicitly rejected

- Traces do NOT "evaporate" on reload: the client id survives, so the log survives
  and continues. Nothing is deleted.
- The API is NOT a state oracle. Its answers about "the current trace" are not
  evidence.
- There is no `epoch` in the de-duplication key.
- There is no per-page-load uuid.

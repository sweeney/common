# `github.com/sweeney/common/wire`

Typed decoders, and for washhouse the encoders too, for MQTT payloads that
more than one service reads. Stdlib only. Each package follows its
publisher's contract, which is normative; the goldens here pin it.

| Package | Publisher and contract | What it has |
|---|---|---|
| `wire/tanktemp` | tanktemp, its `docs/WIRE.md` | `DecodeState`, `DecodeStatus` (with the last will), `DecodeMeter`; copies of tanktemp's goldens, which must stay byte-identical |
| `wire/boiler` | boiler-sensor, its README and wire-contract test | `DecodeEvent`, `DecodeSystem` (status snapshots, compact events, the last will), `System.ReadyHeartbeat` |
| `wire/washhouse` | washhouse, its `docs/WIRE.md` | the payload types, `Encode*` and strict `Decode*` for state, tank, event and status, `LWT`, the topics; the goldens are WIRE.md's examples |

Shared rules, from the contracts:

- **Unknown is `null`, decoded to a nil pointer, never 0.** A key the
  contract says is always present is an error when missing.
- **New fields are additive:** unknown keys are accepted and ignored. A
  field added to `wire/washhouse` after its v1 was frozen is tagged
  `wire:"added"`: always encoded, and decoded as nil when a payload from
  an older publisher lacks it (`testdata/v0.1` keeps those payloads). In
  `wire/washhouse` a key that differs from a known one only in case is an
  error, since `encoding/json` would let it override the known one.
- **Never edit a golden to make a test pass.** A change starts in the
  publisher's contract and its goldens, then comes here.

Consumers: washhouse (all three). statehouse's washhouse adapter will use
`wire/washhouse`; its tanktemp and boiler adapters could move onto the other
two.

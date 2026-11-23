# tanktemp wire goldens (copies)

These files are byte-for-byte copies of tanktemp's wire goldens, from
`sweeney/tanktemp` at commit `c5a5b28`, directory `internal/payload/testdata/`.
tanktemp's `docs/WIRE.md` is normative for them.

Never edit one here to make a test pass. A change starts in tanktemp's
`docs/WIRE.md` and goldens; then copy the new files over and update the
decoder tests. `tanktemp_test.go` checks every `*.golden` in this directory
decodes to the values it expects, so a new golden needs a new expectation.

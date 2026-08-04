# fixture expected-outcome table (test-only)

This file backs `TestTriage_FlagsDeliberateDivergence` — it is not a real
checklist section. Row `b` is **deliberately wrong** on purpose: the
fixture blob's item `b` actually reports `verdict_inputs.http_status: 403`,
but this table claims `200`, so triage must flag a divergence for `b` and
a match for `a`.

| id | pointer | op | expected | meaning |
|---|---|---|---|---|
| a | verdict_inputs.http_status | equals | 200 | item a should be reachable with 200 |
| b | verdict_inputs.http_status | equals | 200 | deliberately wrong expectation: item b's real behavior is 403 (read-only identity), this row exists to prove triage catches a bad expected-outcome entry rather than silently passing |
| a | raw | contains | ok | item a's raw output should mention "ok" |
| c | raw | equals | anything | item c does not exist in the fixture blob — proves item-missing is reported, not silently skipped |

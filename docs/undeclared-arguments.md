# Undeclared arguments

A tool call carrying an argument its tool does not declare is refused. It is not
dropped, and it is not forwarded to an upstream that will ignore it.

## Why it is a refusal

`signoz_aggregate_logs` was called with a `searchText` its schema never declared.
The call returned the count of every log in the window, with `status: success`.

| call | result | rows scanned |
| --- | --- | --- |
| no filter | 8,759,997 | 6,525,190 |
| `searchText='zzzzz-nonexistent-string-qqqq'` | **8,760,201** | 6,525,483 |
| `filter="body CONTAINS 'zzzzz-nonexistent-string-qqqq'"` | 0 | 0 |

A string that cannot appear in any log returned the same total as no filter at
all, and the scan count is the tell: the dropped filter scanned everything.

That is the worst available failure for a filter. An error is caught. A zero is
caught. A large plausible number that is silently the unfiltered total is
indistinguishable from an answer, and a blast-radius estimate was published from
one before a negative control caught it (mcp-beaver#94).

## Where it is enforced

Both paths, because both could produce it:

* **Generated tools.** The schema is umbra's own projection of the operation, so
  it is closed by construction: a name it does not carry is a name the tool does
  not have. `splitArgs` used to skip such a name, with the tool surface *is* the
  schema stated as the reason. The claim was right and the enforcement was a
  silent drop.
* **The passthrough proxy.** The argument map is forwarded verbatim, so an
  argument the upstream does not declare reached it and was ignored there. The
  proxy is the layer that knows the declared surface, so it refuses first.

The proxy reads the declared names off the snapshot taken at startup, the
contract this runtime accepted, rather than off whatever the upstream advertises
at call time.

## Open schemas

An upstream that sets `additionalProperties` to anything other than `false`
keeps the permissive contract it declares, and no refusal applies.

An **absent** `additionalProperties` is treated as closed. That inverts the JSON
Schema default deliberately: the permissive default is what let this through,
and a guard is the layer that is stricter than the thing it guards.

## Pinned parameters

A pinned parameter is absent from the tool schema, so a caller supplying one is
refused rather than silently overruled. The scope holds either way - the
caller's value never reaches the upstream - and refusing also stops the caller
being told its request succeeded when a different one ran. See
[upstream-pins.md](upstream-pins.md).

## What this does not cover

Extra keys **nested inside** a declared property. Those are the body mapping's
business: it projects the named leaves and drops the rest, which is what lets an
Alertmanager webhook post its whole payload at a tool that wants one field. The
control here is over top-level argument names.

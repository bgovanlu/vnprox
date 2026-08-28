# `dashboardTile`

See [plugin-development.md](../plugin-development.md) for the SDK overview,
the stage-only boundary, and the security section this page does not repeat.

## Interface (`internal/plugin/interfaces.go`)

```go
// Tile is one dashboard tile a DashboardTileProvider contributes: a small,
// read-only, named summary datum with an optional deep-link into its owning page,
// mirroring the shape T-904's built-in tiles render client-side. It carries no
// action — a tile is display-only, never a control surface.
type Tile struct {
	// ID is the tile's stable identifier within its provider, for React keying.
	ID string `json:"id"`
	// Title is the tile's display heading.
	Title string `json:"title"`
	// Value is the headline datum, pre-formatted for display.
	Value string `json:"value"`
	// Detail is optional secondary text under the value.
	Detail string `json:"detail,omitempty"`
	// Link is an optional in-app route the tile deep-links to (e.g. "/findings").
	Link string `json:"link,omitempty"`
	// Severity is an optional advisory level ("info"|"warn"|"critical") the UI
	// uses purely for tile coloring; empty means neutral.
	Severity string `json:"severity,omitempty"`
}

// DashboardTileProvider is the dashboard-tile extension point (T-904 becomes
// pluggable). A plugin contributes read-only tiles to the home dashboard. Like
// FindingProducer it is display-only: a tile never issues a mutating request and
// the provider has no change-engine access.
type DashboardTileProvider interface {
	// Tiles returns this provider's current tiles. An error degrades this one
	// provider (its tiles are omitted) without failing the dashboard.
	Tiles(ctx context.Context) ([]Tile, error)
}
```

Minimum capability to attach this point: `netRead`.

## What the host guarantees

- **Called on every dashboard render**, alongside every built-in tile and
  every other installed `dashboardTile` plugin
  (`plugin.Registry.DashboardTiles`).
- **An error degrades only your provider.** A non-nil error from `Tiles`
  drops that provider's tiles from the dashboard response; nothing else on
  the dashboard fails.
- **Your tiles render exactly the fields you set**, with no server-side
  reinterpretation — `Value` is shown pre-formatted, verbatim; format it
  before returning it.

## What the plugin must not do

- **`Tile` carries no action.** There is no click-to-mutate field, no
  button, no form. `Link` is a deep-link into an existing in-app route for a
  human to look at next — it is navigation, not a trigger.
- **Do not put anything sensitive in `Value`/`Detail`.** A tile renders on
  the home dashboard, the first thing every operator sees — do not surface a
  credential, a token, or anything that shouldn't be on-screen at a glance.
- **Do not assume tile ordering.** `ID` scopes a tile within your own
  provider for React keying; it says nothing about placement among other
  providers' tiles.
- **Must not block indefinitely** — `Tiles` shares the dashboard's overall
  render budget with every other provider; respect `ctx.Done()`.

## Minimal working example

From `internal/plugin/plugintest/samples.go` — the SDK's own fixture, real
code exercised by `internal/plugin`'s transport-parity tests:

```go
type sampleTileProvider struct{}

func (sampleTileProvider) Tiles(_ context.Context) ([]plugin.Tile, error) {
	return []plugin.Tile{{
		ID:    SampleTileID,
		Title: "Sample",
		Value: "42",
		Link:  "/topology",
	}}, nil
}
```

Wire it into a `plugin.Manifest` declaring `plugin.ExtDashboardTile` and
`netRead`, and a `plugin.Registration.DashboardTiles` field — the same shape
[`examples/plugin-template/`](https://github.com/bgovanlu/vnprox/tree/main/examples/plugin-template)
uses for `findingProducer`; swap the extension point and the implementation
field.

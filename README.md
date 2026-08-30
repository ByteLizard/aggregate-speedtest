# Aggregate Speedtest

**Your multi-gig line is probably faster than any speedtest says it is.**

Public speedtest servers routinely can't source or sink more than a few Gbps
per client — so on 5/8/10-gig fiber, a single test measures the *server*, not
your line. Run the same test against one server and you'll see 3 Gbps; run it
against another and see 6; your ISP delivers 9. All three results are "real".

This app runs several Ookla speedtests **in parallel against different
servers** and shows you the **aggregate** — the sum across legs — which
approximates what your line can actually move.

Built with [Wails](https://wails.io): Go backend, tiny native shell, no
bundled Chromium.

## Ground rules

- **Hardwired ethernet only.** Wi-Fi bottlenecks long before a multi-gig line
  does, and your NIC must actually be multi-gig (a 1GbE port measures the
  port).
- **Data usage is real**: each leg moves multiple GB per run. Don't run this
  on metered connections.
- Pick 2–4 servers. More legs stop adding signal once your line saturates.
- The **upload sum** is the trustworthy aggregate — download phases across
  legs rarely align perfectly, so read the download sum as a floor.

## Install / run

Requires [Go](https://go.dev) and the [Wails CLI](https://wails.io/docs/gettingstarted/installation):

```
git clone https://github.com/ByteLizard/aggregate-speedtest
cd aggregate-speedtest
wails build
```

The binary lands in `build/bin/`. Or `wails dev` to hack on it.

On first launch the app offers to download the official
[Speedtest® CLI](https://www.speedtest.net/apps/cli) from Ookla (it is not
bundled — it's Ookla's proprietary binary). Using it means accepting
[Ookla's EULA](https://www.speedtest.net/about/eula),
[Terms of Use](https://www.speedtest.net/about/terms) and
[Privacy Policy](https://www.speedtest.net/about/privacy); the app passes
`--accept-license --accept-gdpr` on your behalf after you confirm.

## How it works

1. The Go backend downloads/locates the Ookla CLI (stored in your OS config
   dir), lists nearby servers with `speedtest -L`, and spawns one
   `speedtest -s <id> -f jsonl` process per selected server.
2. Each process streams progress events as JSON lines; the backend forwards
   them to the UI over Wails events.
3. The UI shows live per-leg throughput and the running aggregate; finals use
   each leg's result record.

## Caveats

- This measures *access capacity*, not the experience of any single transfer —
  one TCP flow to one host will still be limited by that host.
- Concurrent legs contend with each other at the far end's discretion; an
  occasional slow leg mid-run is the far server, not your line.
- Not affiliated with Ookla. Speedtest® is a registered trademark of Ookla.

## License

MIT — see [LICENSE](LICENSE). (The Ookla CLI the app downloads has its own
license, which you accept separately.)

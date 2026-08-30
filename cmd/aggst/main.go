// aggst — headless aggregate speedtest.
//
// Runs one Ookla test per server id, all in parallel, and reports the PEAK
// CONCURRENT sum: each leg streams jsonl progress events, the tracker sums
// instantaneous cross-leg rates, and the peak of that sum is the headline.
// Summing per-leg *averages* instead double-counts misaligned phase windows
// and can exceed the physical wire — this method cannot.
//
// Usage:
//
//	aggst 57176 6214 10148
//	aggst -list            # show nearby server ids
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"aggregate-speedtest/internal/ookla"
)

func gbps(bw float64) float64 { return bw * 8 / 1e9 }

type tracker struct {
	mu       sync.Mutex
	down, up map[int]float64
	peakDown float64
	peakUp   float64
	finals   map[int]result
	tty      bool
}

type result struct {
	name string
	down float64
	up   float64
	err  string
}

func (t *tracker) event(id int, ev map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch ev["type"] {
	case "download":
		if d, ok := ev["download"].(map[string]any); ok {
			t.down[id], _ = bandwidth(d)
		}
	case "upload":
		t.down[id] = 0
		if u, ok := ev["upload"].(map[string]any); ok {
			t.up[id], _ = bandwidth(u)
		}
	case "result":
		t.down[id], t.up[id] = 0, 0
		r := result{name: "#" + strconv.Itoa(id)}
		if s, ok := ev["server"].(map[string]any); ok {
			if n, ok := s["name"].(string); ok {
				r.name = n
			}
		}
		if d, ok := ev["download"].(map[string]any); ok {
			bw, _ := bandwidth(d)
			r.down = bw
		}
		if u, ok := ev["upload"].(map[string]any); ok {
			bw, _ := bandwidth(u)
			r.up = bw
		}
		t.finals[id] = r
	default:
		return
	}
	var sd, su float64
	for _, v := range t.down {
		sd += v
	}
	for _, v := range t.up {
		su += v
	}
	t.peakDown = max(t.peakDown, sd)
	t.peakUp = max(t.peakUp, su)
	if t.tty { // live ticker only on a real terminal — it's \r spam in a pipe
		fmt.Printf("\r  live  ↓ %5.2f  ↑ %5.2f Gbps   (peak ↓ %5.2f  ↑ %5.2f)  ", sd, su, t.peakDown, t.peakUp)
	}
}

func bandwidth(section map[string]any) (float64, bool) {
	bw, ok := section["bandwidth"].(float64)
	return gbps(bw), ok
}

func main() {
	list := flag.Bool("list", false, "list nearby servers and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: aggst [-list] <server-id> [server-id ...]\n\n"+
			"Runs parallel Ookla speedtests and reports the peak concurrent sum.\n"+
			"Pick 2-4 nearby servers (each leg moves multiple GB). Hardwired only.\n\n"+
			"Uses Ookla's Speedtest CLI (downloaded from Ookla on first run);\n"+
			"running it accepts Ookla's EULA, Terms of Use and Privacy Policy:\n"+
			"https://www.speedtest.net/about/eula\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	cli := ookla.Find()
	if cli == "" {
		fmt.Fprintln(os.Stderr, "Ookla Speedtest CLI not found — downloading from install.speedtest.net…")
		p, err := ookla.Install()
		if err != nil {
			fmt.Fprintln(os.Stderr, "install failed:", err)
			os.Exit(1)
		}
		cli = p
	}

	if *list {
		out, err := exec.Command(cli, "-L", "--accept-license", "--accept-gdpr").CombinedOutput()
		os.Stdout.Write(out)
		if err != nil {
			os.Exit(1)
		}
		return
	}

	var ids []int
	for _, a := range flag.Args() {
		id, err := strconv.Atoi(a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad server id %q\n", a)
			os.Exit(2)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	t := &tracker{down: map[int]float64{}, up: map[int]float64{}, finals: map[int]result{}}
	if st, err := os.Stdout.Stat(); err == nil {
		t.tty = st.Mode()&os.ModeCharDevice != 0
	}
	fmt.Printf("Running %d parallel legs: %v\n", len(ids), ids)

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cmd := exec.Command(cli, "-s", strconv.Itoa(id), "-f", "jsonl",
				"--accept-license", "--accept-gdpr")
			stdout, err := cmd.StdoutPipe()
			if err == nil {
				err = cmd.Start()
			}
			if err != nil {
				t.mu.Lock()
				t.finals[id] = result{name: "#" + strconv.Itoa(id), err: err.Error()}
				t.mu.Unlock()
				return
			}
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				var ev map[string]any
				if json.Unmarshal(sc.Bytes(), &ev) == nil {
					t.event(id, ev)
				}
			}
			if err := cmd.Wait(); err != nil {
				t.mu.Lock()
				if _, ok := t.finals[id]; !ok {
					t.finals[id] = result{name: "#" + strconv.Itoa(id), err: err.Error()}
				}
				t.mu.Unlock()
			}
		}(id)
	}
	wg.Wait()

	fmt.Println()
	for _, id := range ids {
		r, ok := t.finals[id]
		switch {
		case !ok:
			fmt.Printf("  %6d  no result\n", id)
		case r.err != "":
			fmt.Printf("  %6d  FAILED: %s\n", id, r.err)
		default:
			fmt.Printf("  %6d  %-28s down %5.2f  up %5.2f Gbps\n", id, r.name, r.down, r.up)
		}
	}
	fmt.Printf("\nPEAK CONCURRENT: down %.2f Gbps  up %.2f Gbps\n", t.peakDown, t.peakUp)
}

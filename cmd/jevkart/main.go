// jevkart — a terminal car driven by jev.
//
// Two modes:
//
//	auto    jev drives. It cannot answer within a frame (~200ms vs 60ms), so the
//	        car always reacts on a visible delay. Survival is a readout of latency.
//	manual  you drive with the arrow keys; jev rides along as an advisor and the
//	        game scores how often you ended up where jev said to go.
//
// Tab switches modes at runtime.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"

	"testjev/internal/jev"
)

const (
	lanes      = 3
	trackW     = 54
	carX       = 4
	tick       = 60 * time.Millisecond
	askWithin  = 40.0
	baseSpeed  = 0.42
	spawnEvery = 22
)

type mode int

const (
	modeAuto mode = iota
	modeManual
)

func (m mode) String() string {
	if m == modeAuto {
		return "auto · jev drives"
	}
	return "manual · jev advises"
}

type obstacle struct {
	lane  int
	x     float64
	glyph string
	done  bool
}

type result struct {
	move   string
	conf   float64
	danger float64
	lat    time.Duration
	tokens int
	model  string
	err    error
}

type game struct {
	mode    mode
	carLane int
	obs     []obstacle
	speed   float64
	frame   int

	inFlight  bool
	asked     time.Time
	last      result
	haveLast  bool
	model     string
	decisions int
	tokens    int

	// manual mode: the lane jev's most recent advice pointed at
	adviceLane        int
	agreed, disagreed int

	latSum, latMin, latMax time.Duration

	dodged, crashed int
	flash           int
	note            string
}

func main() {
	bench := flag.Int("bench", 0, "headless: make N sequential calls and report latency, no TUI")
	start := flag.String("mode", "auto", "starting mode: auto (jev drives) or manual (you drive)")
	flag.Parse()

	key, err := loadKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	client := jev.New(key)

	if *bench > 0 {
		runBench(client, *bench)
		return
	}
	m := modeAuto
	if *start == "manual" {
		m = modeManual
	}
	runGame(client, m)
}

// ---------- jev ----------

func ask(client *jev.Client, g *game, out chan<- result) {
	req := jev.Request{
		State: describe(g),
		Questions: map[string]jev.Question{
			"move": {
				Type:         "choice",
				Instructions: "The car must avoid every obstacle. Which lane change keeps it safe?",
				Criteria: map[string]string{
					"up":   "Move one lane up (toward lane 1)",
					"hold": "Stay in the current lane",
					"down": "Move one lane down (toward lane 3)",
				},
			},
			"danger": {
				Type:         "noul",
				Instructions: "Is the car about to hit an obstacle if it does not move?",
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resp, lat, err := client.Ask(ctx, req)
	if err != nil {
		out <- result{lat: lat, err: err}
		return
	}
	out <- result{
		move:   resp.Answers["move"].Choice,
		conf:   resp.Answers["move"].Confidence,
		danger: resp.Answers["danger"].Noul,
		lat:    lat,
		tokens: resp.Usage.InputTokens + resp.Usage.OutputTokens,
		model:  resp.Model,
	}
}

// describe renders the world as the sentence jev reasons over.
func describe(g *game) string {
	var b strings.Builder
	fmt.Fprintf(&b, "A car drives left-to-right on a %d-lane road. The car is in lane %d. ", lanes, g.carLane+1)
	var ahead []string
	for _, o := range g.obs {
		if o.done || o.x < carX {
			continue
		}
		ahead = append(ahead, fmt.Sprintf("an obstacle in lane %d, %d cells ahead", o.lane+1, int(o.x-carX)))
	}
	if len(ahead) == 0 {
		b.WriteString("The road ahead is clear.")
	} else {
		fmt.Fprintf(&b, "Ahead there is %s.", strings.Join(ahead, "; "))
	}
	b.WriteString(" Lane 1 is the top lane and lane 3 is the bottom lane. The car cannot leave the road.")
	return b.String()
}

// ---------- input ----------

// readKeys turns raw bytes into logical key names, including arrow escapes.
func readKeys(out chan<- string) {
	r := bufio.NewReader(os.Stdin)
	for {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		switch b {
		case 'q', 3:
			out <- "quit"
		case 'w', 'k':
			out <- "up"
		case 's', 'j':
			out <- "down"
		case '\t', 'm':
			out <- "toggle"
		case 0x1b:
			b2, err := r.ReadByte()
			if err != nil {
				return
			}
			if b2 != '[' {
				continue
			}
			b3, err := r.ReadByte()
			if err != nil {
				return
			}
			switch b3 {
			case 'A':
				out <- "up"
			case 'B':
				out <- "down"
			}
		}
	}
}

// ---------- game ----------

func runGame(client *jev.Client, m mode) {
	restore := rawMode()
	defer restore()
	fmt.Print("\033[?1049h\033[?25l")
	defer fmt.Print("\033[?25h\033[?1049l")

	keys := make(chan string, 8)
	go readKeys(keys)

	results := make(chan result, 4)
	g := &game{mode: m, carLane: 1, speed: baseSpeed, latMin: time.Hour, adviceLane: -1}
	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		select {
		case k := <-keys:
			switch k {
			case "quit":
				summary(g)
				return
			case "toggle":
				g.toggle()
			case "up":
				if g.mode == modeManual && g.carLane > 0 {
					g.carLane--
				}
			case "down":
				if g.mode == modeManual && g.carLane < lanes-1 {
					g.carLane++
				}
			}
		case r := <-results:
			g.inFlight = false
			g.apply(r)
		case <-t.C:
			g.step()
			if !g.inFlight && g.shouldAsk() {
				g.inFlight = true
				g.asked = time.Now()
				go ask(client, g, results)
			}
			fmt.Print("\033[H" + g.view())
		}
	}
}

func (g *game) toggle() {
	if g.mode == modeAuto {
		g.mode = modeManual
	} else {
		g.mode = modeAuto
	}
	g.adviceLane = -1
}

func (g *game) apply(r result) {
	g.haveLast = true
	g.last = r
	if r.err != nil {
		g.note = "jev error: " + r.err.Error()
		return
	}
	g.note = ""
	g.decisions++
	g.tokens += r.tokens
	g.model = r.model
	g.latSum += r.lat
	if r.lat < g.latMin {
		g.latMin = r.lat
	}
	if r.lat > g.latMax {
		g.latMax = r.lat
	}

	target := g.carLane
	switch r.move {
	case "up":
		if target > 0 {
			target--
		}
	case "down":
		if target < lanes-1 {
			target++
		}
	}
	if g.mode == modeAuto {
		g.carLane = target // jev drives
	} else {
		g.adviceLane = target // jev only advises
	}
}

func (g *game) shouldAsk() bool {
	for _, o := range g.obs {
		if !o.done && o.x > carX && o.x-carX < askWithin {
			return true
		}
	}
	return false
}

func (g *game) step() {
	g.frame++
	if g.flash > 0 {
		g.flash--
	}
	g.speed = baseSpeed + float64(g.frame)/6000.0

	if g.frame%spawnEvery == 0 {
		g.obs = append(g.obs, obstacle{
			lane:  rand.Intn(lanes),
			x:     float64(trackW - 1),
			glyph: []string{"▲", "◆", "▩"}[rand.Intn(3)],
		})
	}
	live := g.obs[:0]
	for _, o := range g.obs {
		prev := o.x
		o.x -= g.speed
		if !o.done && prev >= carX && o.x < carX {
			o.done = true
			if o.lane == g.carLane {
				g.crashed++
				g.flash = 6
			} else {
				g.dodged++
			}
			if g.mode == modeManual && g.adviceLane >= 0 {
				if g.carLane == g.adviceLane {
					g.agreed++
				} else {
					g.disagreed++
				}
			}
		}
		if o.x > -2 {
			live = append(live, o)
		}
	}
	g.obs = live
}

// ---------- render ----------

func (g *game) view() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\033[K\r\n", a...) }

	w("")
	w("  \033[1;36mjev-kart\033[0m  \033[2m%s\033[0m   \033[2m%s\033[0m", g.mode, g.modelLabel())
	w("")
	w("  \033[2m┌%s┐\033[0m", strings.Repeat("─", trackW))

	for i := range lanes {
		row := []rune(strings.Repeat(" ", trackW))
		for _, o := range g.obs {
			if o.done || o.lane != i {
				continue
			}
			if xi := int(o.x); xi >= 0 && xi < trackW {
				row[xi] = []rune(o.glyph)[0]
			}
		}
		// jev's advice shows as a ghost lane marker in manual mode
		if g.mode == modeManual && g.adviceLane == i && i != g.carLane {
			row[carX+1] = '░'
		}

		var body string
		if i == g.carLane {
			car := "\033[1;33m▐█▶\033[0m"
			if g.flash > 0 && g.flash%2 == 0 {
				car = "\033[1;31m▐█▶\033[0m"
			}
			body = colorObs(string(row[:carX])) + car + colorObs(string(row[carX+3:]))
		} else {
			body = colorObs(string(row))
		}
		w("  \033[2m│\033[0m%s\033[2m│\033[0m \033[2m%d\033[0m", body, i+1)
	}
	w("  \033[2m└%s┘\033[0m", strings.Repeat("─", trackW))
	w("")

	switch {
	case g.inFlight:
		w("  \033[2mjev     \033[0m  %s \033[36masking\033[0m  \033[2m%dms\033[0m", spinner(g.frame), time.Since(g.asked).Milliseconds())
	case g.haveLast && g.last.err == nil:
		verb := "drove"
		if g.mode == modeManual {
			verb = "advises"
		}
		w("  \033[2mjev     \033[0m  %s \033[32m%-5s\033[0m \033[2mconf\033[0m %.2f  \033[2mdanger\033[0m %.2f  \033[2m%dms\033[0m",
			verb, g.last.move, g.last.conf, g.last.danger, g.last.lat.Milliseconds())
	default:
		w("  \033[2mjev     \033[0m  \033[2mwaiting for an obstacle…\033[0m")
	}

	if g.decisions > 0 {
		w("  \033[2mlatency \033[0m  avg %dms  min %dms  max %dms  \033[2mn=%d\033[0m",
			(g.latSum / time.Duration(g.decisions)).Milliseconds(),
			g.latMin.Milliseconds(), g.latMax.Milliseconds(), g.decisions)
	} else {
		w("  \033[2mlatency \033[0m  —")
	}

	line := fmt.Sprintf("  \033[2mrun     \033[0m  \033[32m%d dodged\033[0m  \033[31m%d crashed\033[0m  \033[2m%s tokens\033[0m",
		g.dodged, g.crashed, humanK(g.tokens))
	if g.mode == modeManual && g.agreed+g.disagreed > 0 {
		line += fmt.Sprintf("  \033[2m·\033[0m agreed with jev \033[36m%d%%\033[0m",
			100*g.agreed/(g.agreed+g.disagreed))
	}
	w("%s", line)
	w("")
	if g.note != "" {
		w("  \033[31m%s\033[0m", clip(g.note, trackW))
	} else {
		w("  \033[2m[tab] switch mode   [↑/↓] steer (manual)   [q] quit\033[0m")
	}
	return b.String()
}

func colorObs(s string) string {
	s = strings.ReplaceAll(s, "▲", "\033[31m▲\033[0m")
	s = strings.ReplaceAll(s, "◆", "\033[35m◆\033[0m")
	s = strings.ReplaceAll(s, "▩", "\033[33m▩\033[0m")
	s = strings.ReplaceAll(s, "░", "\033[2;36m░\033[0m")
	return s
}

func spinner(f int) string { return string([]rune("◐◓◑◒")[(f/3)%4]) }

func (g *game) modelLabel() string {
	if g.model == "" {
		return "connecting…"
	}
	return g.model
}

func humanK(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func summary(g *game) {
	fmt.Print("\033[?25h\033[?1049l")
	fmt.Printf("\njev-kart — %d decisions via %s\n", g.decisions, g.modelLabel())
	if g.decisions > 0 {
		fmt.Printf("latency: avg %dms  min %dms  max %dms\n",
			(g.latSum / time.Duration(g.decisions)).Milliseconds(),
			g.latMin.Milliseconds(), g.latMax.Milliseconds())
	}
	fmt.Printf("road:    %d dodged, %d crashed, %s tokens\n", g.dodged, g.crashed, humanK(g.tokens))
	if g.agreed+g.disagreed > 0 {
		fmt.Printf("advice:  agreed with jev %d%% (%d of %d)\n",
			100*g.agreed/(g.agreed+g.disagreed), g.agreed, g.agreed+g.disagreed)
	}
}

// ---------- bench ----------

func runBench(client *jev.Client, n int) {
	g := &game{carLane: 1, obs: []obstacle{{lane: 1, x: 18}, {lane: 0, x: 31}}}
	fmt.Printf("state: %s\n\n", describe(g))
	var sum time.Duration
	min, max := time.Hour, time.Duration(0)
	ok := 0
	for i := range n {
		out := make(chan result, 1)
		ask(client, g, out)
		r := <-out
		if r.err != nil {
			fmt.Printf("%2d  ERROR %v\n", i+1, r.err)
			continue
		}
		ok++
		sum += r.lat
		if r.lat < min {
			min = r.lat
		}
		if r.lat > max {
			max = r.lat
		}
		fmt.Printf("%2d  %4dms  move=%-5s conf=%.2f danger=%.2f  %s\n",
			i+1, r.lat.Milliseconds(), r.move, r.conf, r.danger, r.model)
	}
	if ok > 0 {
		fmt.Printf("\n%d/%d ok — avg %dms  min %dms  max %dms\n",
			ok, n, (sum / time.Duration(ok)).Milliseconds(), min.Milliseconds(), max.Milliseconds())
	}
}

// ---------- plumbing ----------

func loadKey() (string, error) {
	if k := os.Getenv("API_KEY"); k != "" {
		return k, nil
	}
	for _, p := range []string{".env", "/data/testJev/.env"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			if strings.HasPrefix(line, "API_KEY=") {
				return strings.Trim(strings.TrimPrefix(line, "API_KEY="), `"'`), nil
			}
		}
	}
	return "", fmt.Errorf("no API_KEY in environment or .env")
}

func rawMode() func() {
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return func() {}
	}
	exec.Command("stty", "-F", "/dev/tty", "raw", "-echo").Run()
	return func() { exec.Command("stty", "-F", "/dev/tty", "sane").Run() }
}

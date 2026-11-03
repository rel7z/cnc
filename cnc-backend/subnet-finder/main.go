package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"
)

type ProgressUI struct {
	totalDomains int64
	resolvedIPs  int64
	fetchedCIDRs int64
	pingedIPs    int64
	validIPs     int64
	startTime    time.Time
	done         chan struct{}
}

func NewProgressUI() *ProgressUI {
	return &ProgressUI{
		startTime: time.Now(),
		done:      make(chan struct{}),
	}
}

func (p *ProgressUI) Start() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.render()
		case <-p.done:
			p.renderFinal()
			return
		}
	}
}

func (p *ProgressUI) Stop() {
	close(p.done)
}

func (p *ProgressUI) render() {
	dom := atomic.LoadInt64(&p.totalDomains)
	res := atomic.LoadInt64(&p.resolvedIPs)
	cidrs := atomic.LoadInt64(&p.fetchedCIDRs)
	pinged := atomic.LoadInt64(&p.pingedIPs)
	valid := atomic.LoadInt64(&p.validIPs)
	elapsed := time.Since(p.startTime).Round(time.Second)

	fmt.Fprintf(os.Stderr, "\r[*] Domains: %d/%d | CIDRs: %d | Pinged: %d | Alive: %d | Elapsed: %v   ",
		res, dom, cidrs, pinged, valid, elapsed)
}

func (p *ProgressUI) renderFinal() {
	valid := atomic.LoadInt64(&p.validIPs)
	elapsed := time.Since(p.startTime).Round(time.Second)
	fmt.Fprintf(os.Stderr, "\n[+] Done. Found %d valid alive IPs in %v\n", valid, elapsed)
}

func main() {
	var input, output string
	var concurrency, memoryMB, maxAddresses int
	var timeout time.Duration
	flag.StringVar(&input, "file", "", "File containing domains or CIDRs")
	flag.StringVar(&input, "l", "", "File containing domains or CIDRs")
	flag.StringVar(&output, "out", "valid.txt", "Output file for alive IPs")
	flag.IntVar(&concurrency, "c", runtime.GOMAXPROCS(0)*16, "Maximum concurrent probes")
	flag.IntVar(&memoryMB, "memory-mb", 128, "Go memory target in MiB (disk-backed deduplication)")
	flag.IntVar(&maxAddresses, "max-addresses", 4096, "Maximum addresses in an IPv4 or IPv6 subnet")
	flag.DurationVar(&timeout, "timeout", 2*time.Second, "DNS and ping timeout")
	flag.Parse()
	if input == "" || concurrency < 1 || memoryMB < 32 || memoryMB > 4096 || maxAddresses < 1 || maxAddresses > 65536 || timeout <= 0 {
		fmt.Fprintln(os.Stderr, "Usage: subnet-finder -l input.txt [-c 100] [-timeout 2s] [-out valid.txt]")
		os.Exit(1)
	}
	// Bound overhead even when a caller supplies an excessive concurrency value.
	if ceiling := min(1024, memoryMB*2); concurrency > ceiling {
		fmt.Fprintf(os.Stderr, "[!] Concurrency capped at %d for the memory target\n", ceiling)
		concurrency = ceiling
	}
	debug.SetMemoryLimit(int64(memoryMB) << 20)
	in, err := os.Open(input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Input:", err)
		os.Exit(1)
	}
	defer in.Close()
	inputInfo, err := in.Stat()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Input:", err)
		os.Exit(1)
	}
	if outputInfo, err := os.Stat(output); err == nil && os.SameFile(inputInfo, outputInfo) {
		fmt.Fprintln(os.Stderr, "Input and output must be different files")
		os.Exit(1)
	}
	out, err := os.Create(output)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Output:", err)
		os.Exit(1)
	}
	defer out.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ui := NewProgressUI()
	uiDone := make(chan struct{})
	go func() { ui.Start(); close(uiDone) }()
	err = runPipeline(ctx, in, io.MultiWriter(out, os.Stdout), pipelineOptions{Concurrency: concurrency, Timeout: timeout, MaxAddresses: maxAddresses, StateDir: os.TempDir()}, defaultDependencies(timeout), ui)
	ui.Stop()
	<-uiDone
	if err != nil {
		fmt.Fprintln(os.Stderr, "Enumeration:", err)
		os.Exit(1)
	}
}

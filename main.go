package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/signal"

	"github.com/hypebeast/go-osc/osc"
	"go.uber.org/zap"
	yaml "gopkg.in/yaml.v2"
)

var debug bool
var listenAddr string
var configFile string

var logger *zap.Logger

// Config represents a repeater configuration.
type Config struct {
	ListenPorts []int             `yaml:"listenPorts"`
	Targets     []*netip.AddrPort `yaml:"targets"`
}

func init() {
	flag.BoolVar(&debug, "debug", false, "debug logging")
	flag.StringVar(&configFile, "c", "config.yaml", "configuration map")
}

func main() {
	var err error

	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if debug {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}

	if err != nil {
		log.Fatalln("failed to create logger:", err)
	}

	logger.Debug("Debug mode enabled")

	cfg := new(Config)

	f, err := os.Open(configFile)
	if err != nil {
		logger.Sugar().Fatalf("failed to open config file %s: %s", configFile, err.Error())
	}

	if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
		logger.Sugar().Fatalf("failed to parse config file %s: %s", configFile, err.Error())
	}

	d := NewDistributor(ctx, cfg.Targets)

	for _, p := range cfg.ListenPorts {
		_, err := NewReceiver(ctx, fmt.Sprintf(":%d", p), d)
		if err != nil {
			logger.Sugar().Fatal("failed to start receiver:", err)
		}
	}

	<-ctx.Done()
}

// Distributor provides an exchange in which anything sent to it is sent ot any targets registered to it.
type Distributor struct {
	senders []*Sender
	ch      chan *osc.Message
}

func NewDistributor(ctx context.Context, targets []*netip.AddrPort) *Distributor {
	var senders []*Sender

	for _, t := range targets {
		senders = append(senders, NewSender(ctx, osc.NewClient(t.Addr().String(), int(t.Port()))))
	}

	d := &Distributor{
		senders: senders,
		ch:      make(chan *osc.Message),
	}

	go d.run(ctx)

	return d
}

func (d *Distributor) run(ctx context.Context) {
	for {
		select {
		case m := <-d.ch:
			logger.Debug("received message", zap.String("msg", m.String()))
			for _, s := range d.senders {
				s.Send(m)
			}
		case <-ctx.Done():
			return
		}
	}
}

// Send transmits a message to the Distributor for distribution.
func (d *Distributor) Send(m *osc.Message) {
	select {
	case d.ch <- m:
	default:
	}
}

// Sender ships a message received to its designated target, buffering as necessary, dropping if need be.  It will never block.
type Sender struct {
	ch chan *osc.Message
	t  *osc.Client
}

func NewSender(ctx context.Context, target *osc.Client) *Sender {
	s := &Sender{
		ch: make(chan *osc.Message, 100),
		t:  target,
	}

	go s.run(ctx)

	return s
}

func (s *Sender) run(ctx context.Context) {
	logger.Sugar().Infof("starting new Sender to %s:%d", s.t.IP(), s.t.Port())

	for {
		select {
		case m := <-s.ch:
			logger.Debug("sending message",
				zap.String("msg", m.String()),
				zap.String("target", fmt.Sprintf("%s:%d", s.t.IP(), s.t.Port())),
			)

			s.t.Send(m)
		case <-ctx.Done():
			return
		}
	}
}

// Send sends a message to the target of the Sender.
func (s *Sender) Send(m *osc.Message) {
	select {
	case s.ch <- m:
	default:
	}
}

// Receiver listens on a port for OSC messages, sending any received messages to the Distrubtor.
type Receiver struct {
	d *Distributor
	s *osc.Server
}

// NewReceiver creates a new OSC receiver
func NewReceiver(ctx context.Context, addr string, d *Distributor) (*Receiver, error) {
	disp := osc.NewStandardDispatcher()

	disp.AddMsgHandler("*", func(msg *osc.Message) {
		d.Send(msg)
	})

	svr := &osc.Server{
		Addr:       addr,
		Dispatcher: disp,
	}

	go func() {
		logger.Sugar().Info("starting receiver on", addr)

		if err := svr.ListenAndServe(); err != nil {
			logger.Sugar().Fatalf("failed to start server on %s: %s", addr, err.Error())
		}
	}()

	return &Receiver{
		d: d,
		s: svr,
	}, nil
}

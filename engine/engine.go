package engine

import (
	"fmt"
	glog "log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yosisa/fluxion/buffer"
	"github.com/yosisa/fluxion/log"
	"github.com/yosisa/fluxion/message"
	"github.com/yosisa/fluxion/pipe"
	"github.com/yosisa/fluxion/plugin"
	"github.com/yosisa/pave/process"
)

type Engine struct {
	pm      *process.ProcessManager
	plugins map[string]*Instance
	embeds  []*Instance
	units   []*ExecUnit
	filters []*ExecUnit
	tr      map[string]*TagRouter
	ftr     *TagRouter
	bufs    map[string]*buffer.Options
	unitID  int32
	log     *log.Logger
	stopped chan struct{}
}

func New() *Engine {
	defaultBuf := &buffer.Options{}
	defaultBuf.SetDefault()
	e := &Engine{
		pm:      process.NewProcessManager(process.StrategyRestartOnError, 3*time.Second),
		plugins: make(map[string]*Instance),
		tr:      make(map[string]*TagRouter),
		ftr:     &TagRouter{},
		bufs: map[string]*buffer.Options{
			"default": defaultBuf,
		},
		stopped: make(chan struct{}),
	}
	e.log = &log.Logger{
		Name:     "engine",
		Prefix:   "[engine] ",
		EmitFunc: e.Filter,
	}
	return e
}

func (e *Engine) RegisterBuffer(opts *buffer.Options) {
	opts.SetDefault()
	e.bufs[opts.Name] = opts
}

func (e *Engine) pluginInstance(name string) *Instance {
	if ins, ok := e.plugins[name]; ok {
		return ins
	}
	ins := NewInstance(name, e)
	e.plugins[name] = ins

	if f, ok := plugin.EmbeddedPlugins[name]; ok {
		p1 := pipe.NewInProcess()
		p2 := pipe.NewInProcess()
		ins.rp = p1
		ins.wp = p2
		go plugin.New(name, f).RunWithPipe(p2, p1)
		e.embeds = append(e.embeds, ins)
	} else {
		e.pm.Add(process.New("fluxion-"+name, prepareFuncFactory(ins), func(err error) {
			e.log.Criticalf("%s plugin crashed: %v", name, err)
		}))
	}
	return ins
}

func (e *Engine) addExecUnit(ins *Instance, conf map[string]interface{}, bopts *buffer.Options) *ExecUnit {
	unit := ins.AddExecUnit(atomic.AddInt32(&e.unitID, 1), conf, bopts)
	e.units = append(e.units, unit)
	return unit
}

func (e *Engine) RegisterInputPlugin(conf map[string]interface{}) {
	ins := e.pluginInstance("in-" + conf["type"].(string))
	e.addExecUnit(ins, conf, nil)
}

func (e *Engine) RegisterOutputPlugin(name string, conf map[string]interface{}) error {
	bufName := "default"
	if name, ok := conf["buffer"].(string); ok {
		bufName = name
	}
	buf, ok := e.bufs[bufName]
	if !ok {
		return fmt.Errorf("No such buffer defined: %s", bufName)
	}

	ins := e.pluginInstance("out-" + conf["type"].(string))
	unit := e.addExecUnit(ins, conf, buf)

	tr, ok := e.tr[name]
	if !ok {
		tr = &TagRouter{}
		e.tr[name] = tr
	}
	re, err := regexp.Compile(conf["match"].(string))
	if err != nil {
		return err
	}
	tr.Add(re, unit)
	return nil
}

func (e *Engine) RegisterFilterPlugin(conf map[string]interface{}) error {
	ins := e.pluginInstance("filter-" + conf["type"].(string))
	unit := e.addExecUnit(ins, conf, nil)

	re, err := regexp.Compile(conf["match"].(string))
	if err != nil {
		return err
	}
	e.ftr.Add(re, unit)

	// Register new filter to the preceding filters
	for _, f := range e.filters {
		f.Router.Add(re, unit)
	}
	e.filters = append(e.filters, unit)
	return nil
}

func (e *Engine) Filter(ev *message.Event) {
	if ins := e.ftr.Route(ev.Tag); ins != nil {
		ins.Emit(ev)
	} else {
		e.Emit(ev)
	}
}

func (e *Engine) Emit(ev *message.Event) {
	for _, tr := range e.tr {
		if ins := tr.Route(ev.Tag); ins != nil {
			ins.Emit(ev)
		}
	}
}

func (e *Engine) Start() {
	for _, p := range e.embeds {
		p.Start()
	}
	e.pm.Start()
	go e.signalHandler()
}

func (e *Engine) Wait() {
	<-e.stopped
}

func (e *Engine) Stop() {
	time.AfterFunc(10*time.Second, e.pm.Stop)
	e.stopPlugins("in-")
	e.stopPlugins("filter-")
	e.stopPlugins("out-")
	e.pm.Wait()
	close(e.stopped)
}

func (e *Engine) stopPlugins(prefix string) {
	var wg sync.WaitGroup
	for name, ins := range e.plugins {
		if strings.HasPrefix(name, prefix) {
			wg.Add(1)
			go func(name string, ins *Instance) {
				ins.Stop()
				glog.Printf("%s plugin stopped", name)
				wg.Done()
			}(name, ins)
		}
	}
	wg.Wait()
}

func (e *Engine) signalHandler() {
	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
	for _ = range c {
		e.Stop()
		signal.Stop(c)
		close(c)
	}
}

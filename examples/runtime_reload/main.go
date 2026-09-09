// Command runtime_reload demonstrates caller-owned configuration loading,
// immutable publication, local provider fallback, and in-memory telemetry.
package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"sync"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
	"github.com/imbrooklyn/rulite/dynamic"
	ruliteotel "github.com/imbrooklyn/rulite/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

//go:embed testdata/*.json
var sources embed.FS

type payment struct {
	AmountMinor  int64
	Provider     string
	AppliedBy    rulite.RuleID
	Attempts     []string
	Audit        string
	AuditVersion rulite.RuleSetVersion
}

type providerParams struct {
	Provider string `json:"provider"`
}

var errUnavailable = errors.New("provider unavailable")

type loader struct {
	conditions *cel.Compiler[payment]
	actions    *dynamic.Registry[payment]
}

func newLoader(attempt func(context.Context, *payment, string) error) (*loader, error) {
	conditions, err := cel.NewCompiler[payment]("input")
	if err != nil {
		return nil, err
	}
	actions := dynamic.NewRegistry[payment]()
	for _, name := range []string{"primary", "secondary"} {
		id := rulite.RuleID("payment/" + name)
		err := actions.Register("payment."+name+"/v1", func(ctx context.Context, p *payment, params providerParams) error {
			if err := attempt(ctx, p, params.Provider); err != nil {
				return err
			}
			if p.Provider == "" {
				p.Provider, p.AppliedBy = params.Provider, id
			}
			return nil
		}, func(params providerParams) error {
			if params.Provider != name+"/v1" && params.Provider != name+"/v2" {
				return errors.New("unsupported provider configuration")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if err := actions.Freeze(); err != nil {
		return nil, err
	}
	return &loader{conditions: conditions, actions: actions}, nil
}

func (l *loader) compile(source []byte, version rulite.RuleSetVersion) (*rulite.Engine[payment], error) {
	definitions, err := dynamic.Decode(source)
	if err != nil {
		return nil, err
	}
	members, err := dynamic.CompileRules(definitions, l.conditions, l.actions)
	if err != nil {
		return nil, err
	}
	manual := rulite.NewRule[payment]("payment/manual").Priority(-100).
		When(func(_ context.Context, p *payment) (bool, error) { return len(p.Attempts) > 0, nil }).
		Then(func(_ context.Context, p *payment) error {
			p.Provider, p.AppliedBy = "manual", "payment/manual"
			return nil
		})
	providers := rulite.FirstFireGroup("payment/providers", append(members, manual)...)
	audit := rulite.NewRule[payment]("payment/audit").Priority(-100).
		When(func(_ context.Context, p *payment) (bool, error) { return p.Provider != "", nil }).
		Then(func(_ context.Context, p *payment) error {
			p.Audit, p.AuditVersion = p.Provider+":"+string(p.AppliedBy), version
			return nil
		})
	set, err := rulite.CompileEntries(providers.Entry(), audit.Entry())
	if err != nil {
		return nil, err
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(source))
	set, err = set.WithIdentity(version, rulite.SourceDigest(digest))
	if err != nil {
		return nil, err
	}
	return rulite.NewEngineFromRuleSet(set, rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError)))
}

func (l *loader) reload(runtime *rulite.Runtime[payment], source []byte, version rulite.RuleSetVersion) (rulite.SnapshotInfo, error) {
	engine, err := l.compile(source, version)
	if err != nil {
		return rulite.SnapshotInfo{}, err
	}
	return runtime.Publish(engine)
}

func localAttempt(_ context.Context, p *payment, provider string) error {
	p.Attempts = append(p.Attempts, provider)
	if provider == "primary/v1" || provider == "primary/v2" {
		return errUnavailable
	}
	return nil
}

func run() error {
	ctx := context.Background()
	l, err := newLoader(localAttempt)
	if err != nil {
		return err
	}
	oldSource, err := sources.ReadFile("testdata/v1.json")
	if err != nil {
		return err
	}
	initial, err := l.compile(oldSource, "payments/v1")
	if err != nil {
		return err
	}
	runtime, err := rulite.NewRuntime(initial)
	if err != nil {
		return err
	}
	reader := sdkmetric.NewManualReader()
	metrics := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	exporter := tracetest.NewInMemoryExporter()
	traces := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer metrics.Shutdown(ctx)
	defer traces.Shutdown(ctx)
	adapter, err := ruliteotel.New(traces, metrics, ruliteotel.WithTraceVersions(), ruliteotel.WithEventLimit(4))
	if err != nil {
		return err
	}
	// Acquisition and the publication decision belong to this application.
	next, err := sources.ReadFile("testdata/v2.json")
	if err != nil {
		return err
	}
	if _, err := l.reload(runtime, next, "payments/v2"); err != nil {
		return err
	}
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			input := payment{AmountMinor: 100}
			result, err := adapter.Fire(ctx, runtime, &input, rulite.WithTrace())
			if !errors.Is(err, errUnavailable) || result.Counts().Failed != 1 || input.Provider != "secondary/v2" || input.AppliedBy != "payment/secondary" || input.AuditVersion != "payments/v2" || result.Snapshot().Revision() != 2 || len(result.Diagnostics()) != 1 || !errors.Is(result.Diagnostics()[0], ruliteotel.ErrEventLimit) {
				results <- errors.New("unexpected payment execution")
				return
			}
			results <- nil
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			return err
		}
	}
	fmt.Println("Completed: 4 payments; version: payments/v2; revision: 2")
	fmt.Println("Provider: secondary/v2; AppliedBy: payment/secondary; audit completed")
	fmt.Println("Each result retains one provider failure and one independent trace-limit diagnostic")
	return nil
}

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

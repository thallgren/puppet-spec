package pspec

import (
	. "github.com/puppetlabs/go-evaluator/evaluator"
	. "github.com/puppetlabs/go-evaluator/types"
	"reflect"
	. "github.com/puppetlabs/go-pspec/testutils"
	. "github.com/puppetlabs/go-evaluator/eval"
	. "github.com/puppetlabs/go-evaluator/pcore"
	.	"github.com/puppetlabs/go-parser/issue"
)

type (

	Input interface {
		CreateTests(expected Result) []Executable
	}

	Node interface {
		Description() string
		Get(key string) (LazyValue, bool)
		CreateTest() Test
		collectInputs(context *TestContext, inputs []Input) []Input
	}

	Result interface {
		CreateTest(actual interface{}) Executable

		setExample(example *Example)
	}

	ResultEntry interface {
		Match() PValue
	}

	node struct {
		description string
		values map[string]LazyValue
		given       *Given
	}

	Example struct {
		node
		result      Result
		evaluator   Evaluator
	}

	Examples struct {
		node
		children    []Node
	}

	Given struct {
		inputs []Input
	}

	ParseResult struct {
		example  *Example
		expected string
	}

	EvaluationResult struct {
		example  *Example
		expected PValue
	}

	Source struct {
		sources []string
	}

	SettingsInput struct {
		settings PValue
	}

	ScopeInput struct {
		scope PValue
	}
)


func (e *EvaluationResult) CreateTest(actual interface{}) Executable {
	source := actual.(string)

	return func(context *TestContext, assertions Assertions) {
		actual, issues := parseAndValidate(source, false)
		failOnError(assertions, issues)
		actualResult, evalIssues := evaluate(e.example.Evaluator(), actual, context.Scope())
		failOnError(assertions, evalIssues)
		assertions.AssertEquals(e.expected, actualResult)
	}
}

func (e *EvaluationResult) setExample(example *Example) {
	e.example = example
}

func (n *node) initialize(description string, given *Given) {
	n.description = description
	n.given = given
	n.values = make(map[string]LazyValue, 8)
}

func (n *node) addLetDefs(lazyValueLets []*LazyValueLet) {
	for _, ll := range lazyValueLets {
		n.values[ll.valueName] = ll.value
	}
}

func newExample(description string, given *Given, result Result) *Example {
	e := &Example{result:result}
	e.node.initialize(description, given)
	return e
}

func newExamples(description string, given *Given, children []Node) *Examples {
	e := &Examples{children:children}
	e.node.initialize(description, given)
	return e
}

func (e *node) collectInputs(context *TestContext, inputs []Input) []Input {
	pc := context.parent
	if pc != nil {
		inputs = pc.node.collectInputs(pc, inputs)
	}
	g := e.given
	if g != nil {
		inputs = append(inputs, g.inputs...)
	}
	return inputs
}

func (e *Example) CreateTest() Test {
	test := func(context *TestContext, assertions Assertions) {
		tests := make([]Executable, 0, 8)
		for _, input := range e.collectInputs(context, make([]Input, 0, 8)) {
			tests = append(tests, input.CreateTests(e.result)...)
		}
		for _, test := range tests {
			test(context, assertions)
		}
	}
	return &TestExecutable{testNode{e}, test}
}

func (e *Example) Evaluator() Evaluator {
	if e.evaluator == nil {
		e.evaluator = NewEvaluator(NewParentedLoader(Puppet.EnvironmentLoader()), NewArrayLogger())
	}
	return e.evaluator
}

func (e *Examples) CreateTest() Test {
	tests := make([]Test, len(e.children))
	for idx, child := range e.children {
		tests[idx] = child.CreateTest()
	}
	return &TestGroup{testNode{e}, tests}
}

func (n *node) Description() string {
	return n.description
}

func (n *node) Get(key string) (v LazyValue, ok bool) {
	v, ok = n.values[key]
	return
}

func (p *ParseResult) CreateTest(actual interface{}) Executable {
	source := actual.(string)
	expectedPN := ParsePN(``, p.expected)

	return func(context *TestContext, assertions Assertions) {
		actual, issues := parseAndValidate(source, true)
		failOnError(assertions, issues)
		actualPN := actual.ToPN()
		assertions.AssertEquals(expectedPN.String(), actualPN.String())
	}
}

func (p *ParseResult) setExample(example *Example) {
	p.example = example
}

func (s *SettingsInput) CreateTests(expected Result) []Executable {
	// Settings input does not create any tests
	return []Executable{func(tc *TestContext, assertions Assertions) {
		settings, ok := tc.resolveLazyValues(s.settings).(*HashValue)
		if !ok {
			Error(PSPEC_VALUE_NOT_HASH, H{`type`:`Settings`})
		}
		p := Puppet
		for _, e := range settings.EntriesSlice() {
			p.Set(e.Key().String(), e.Value())
		}
	}}
}

func (s *ScopeInput) CreateTests(expected Result) []Executable {
	return []Executable{func(tc *TestContext, assertions Assertions) {
		scope, ok := tc.resolveLazyValues(s.scope).(*HashValue)
		if !ok {
			Error(PSPEC_VALUE_NOT_HASH, H{`type`:`Scope`})
		}
		tc.scope = NewScope2(scope)
	}}
}

func (i *Source) CreateTests(expected Result) []Executable {
	result := make([]Executable, len(i.sources))
	for idx, source := range i.sources {
		result[idx] = expected.CreateTest(source)
	}
	return result
}

func init() {
	NewGoConstructor2(`Example`,
		func(l LocalTypes) {
			l.Type(`Given`, `Runtime[go, '*pspec.Given']`)
			l.Type(`Let`, `Runtime[go, '*pspec.LazyValueLet']`)
			l.Type2(`Result`, NewRuntimeType3(reflect.TypeOf([]Result{}).Elem()))
		},
		func(d Dispatch) {
			d.Param(`String`)
			d.RepeatedParam(`Variant[Let,Given,Result]`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				lets := make([]*LazyValueLet, 0)
				var given *Given
				var result Result
				for _, arg := range args[1:] {
					if rt, ok := arg.(*RuntimeValue); ok {
						i := rt.Interface()
						switch i.(type) {
						case *LazyValueLet:
							lets = append(lets, i.(*LazyValueLet))
						case *Given:
							if given != nil {

							}
							given = i.(*Given)
						case Result:
							result = i.(Result)
						}
					}
				}
				example := newExample(args[0].String(), given, result)
				example.addLetDefs(lets)
				result.setExample(example)
				return WrapRuntime(example)
			})
		})

	NewGoConstructor2(`Examples`,
		func(l LocalTypes) {
			l.Type(`Given`, `Runtime[go, '*pspec.Given']`)
			l.Type(`Let`, `Runtime[go, '*pspec.LazyValueLet']`)
			l.Type2(`Node`, NewRuntimeType3(reflect.TypeOf([]Node{}).Elem()))
			l.Type(`Nodes`, `Variant[Node, Array[Nodes]]`)
		},
		func(d Dispatch) {
			d.Param(`String`)
			d.RepeatedParam(`Variant[Nodes,Let,Given]`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				lets := make([]*LazyValueLet, 0)
				var given *Given
				others := make([]PValue, 0)
				for _, arg := range args[1:] {
					if rt, ok := arg.(*RuntimeValue); ok {
						if l, ok := rt.Interface().(*LazyValueLet); ok {
							lets = append(lets, l)
							continue
						}
						if g, ok := rt.Interface().(*Given); ok {
							given = g
							continue
						}
					}
					others = append(others, arg)
				}
				ex := newExamples(args[0].String(), given, splatNodes(others))
				ex.addLetDefs(lets)
				return WrapRuntime(ex)
			})
		})

	NewGoConstructor(`Given`,
		func(d Dispatch) {
			d.RepeatedParam2(NewVariantType2(DefaultStringType(), NewRuntimeType3(reflect.TypeOf([]Input{}).Elem())))
			d.Function(func(c EvalContext, args []PValue) PValue {
				argc := len(args)
				inputs := make([]Input, argc)
				for idx := 0; idx < argc; idx++ {
					arg := args[idx]
					if str, ok := arg.(*StringValue); ok {
						inputs[idx] = &Source{[]string{str.String()}}
					} else {
						inputs[idx] = arg.(*RuntimeValue).Interface().(Input)
					}
				}
				return WrapRuntime(&Given{inputs})
			})
		})

	NewGoConstructor(`Settings`,
		func(d Dispatch) {
			d.Param(`Any`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				return WrapRuntime(&SettingsInput{args[0]})
			})
		})

	NewGoConstructor(`Scope`,
		func(d Dispatch) {
			d.Param(`Hash[Pattern[/\A[a-z_]\w*\z/],Any]`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				return WrapRuntime(&ScopeInput{args[0]})
			})
		})

	NewGoConstructor(`Source`,
		func(d Dispatch) {
			d.RepeatedParam(`String`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				argc := len(args)
				sources := make([]string, argc)
				for idx := 0; idx < argc; idx++ {
					sources[idx] = args[idx].String()
				}
				return WrapRuntime(&Source{sources})
			})
		})

	NewGoConstructor(`Unindent`,
		func(d Dispatch) {
			d.Param(`String`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				return WrapString(Unindent(args[0].String()))
			})
		})
}

func splatNodes(args []PValue) []Node {
	nodes := make([]Node, 0)
	for _, arg := range args {
		if rv, ok := arg.(*RuntimeValue); ok {
			nodes = append(nodes, rv.Interface().(Node))
		} else {
			nodes = append(nodes, splatNodes(arg.(*ArrayValue).Elements())...)
		}
	}
	return nodes
}

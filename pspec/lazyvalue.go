package pspec

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/puppetlabs/go-evaluator/eval"
	. "github.com/puppetlabs/go-evaluator/evaluator"
	. "github.com/puppetlabs/go-evaluator/types"
	"github.com/puppetlabs/go-parser/issue"
	"github.com/puppetlabs/go-parser/parser"
)

type (
	LazyValue interface {
		Get(tc *TestContext) PValue
		Id() int64
	}

	LazyValueGet struct {
		valueName string
	}

	LazyValueLet struct {
		valueName string
		value     LazyValue
	}

	lazyValue struct {
		id int64
	}

	GenericValue struct {
		lazyValue
		content PValue
	}

	DirectoryValue struct {
		lazyValue
		content PValue
	}

	FileValue struct {
		lazyValue
		content PValue
	}

	QuoteValue struct {
		lazyValue
		text PValue
	}

	LazyScope struct {
		eval.BasicScope
		ctx *TestContext
	}
)

var nextLazyId = int64(0)

func (lv *lazyValue) initialize() {
	lv.id = atomic.AddInt64(&nextLazyId, 1)
}

func (lv *lazyValue) Id() int64 {
	return lv.id
}

func newGenericValue(content PValue) *GenericValue {
	d := &GenericValue{content: content}
	d.lazyValue.initialize()
	return d
}

func (lg *LazyValueGet) Get(tc *TestContext) PValue {
	if lv, ok := tc.getLazyValue(lg.valueName); ok {
		return tc.Get(lv)
	}
	panic(Error(PSPEC_GET_OF_UNKNOWN_VARIABLE, issue.H{`name`: lg.valueName}))
}

func (gv *GenericValue) Get(tc *TestContext) PValue {
	return tc.resolveLazyValues(gv.content)
}

func newDirectoryValue(content PValue) *DirectoryValue {
	d := &DirectoryValue{content: content}
	d.lazyValue.initialize()
	return d
}

func (dv *DirectoryValue) Get(tc *TestContext) PValue {
	tmpDir, err := ioutil.TempDir(``, `pspec`)
	if err != nil {
		panic(err)
	}
	dir, ok := tc.resolveLazyValues(dv.content).(*HashValue)
	if !ok {
		panic(Error(PSPEC_VALUE_NOT_HASH, issue.H{`type`: `Directory`}))
	}
	makeDirectories(tmpDir, dir)
	tc.registerTearDown(func() {
		err := os.RemoveAll(tmpDir)
		if err != nil {
			panic(err)
		}
	})
	return WrapString(tmpDir)
}

func newFileValue(content PValue) *FileValue {
	d := &FileValue{content: content}
	d.lazyValue.initialize()
	return d
}

func (dv *FileValue) Get(tc *TestContext) PValue {
	tmpFile, err := ioutil.TempFile(``, `pspec`)
	if err != nil {
		panic(err)
	}
	path := tmpFile.Name()
	writeFileValue(path, tc.resolveLazyValues(dv.content))
	tc.registerTearDown(func() {
		err := os.Remove(path)
		if err != nil {
			panic(err)
		}
	})
	return WrapString(path)
}

func newQuoteValue(text PValue) *QuoteValue {
	d := &QuoteValue{text: text}
	d.lazyValue.initialize()
	return d
}

func (q *QuoteValue) Get(tc *TestContext) PValue {
	text, ok := tc.resolveLazyValues(q.text).(*StringValue)
	if !ok {
		panic(Error(PSPEC_QUOTE_NOT_STRING, issue.NO_ARGS))
	}

	ex, err := parser.CreateParser().Parse(``, `"`+text.String()+`"`, true)
	if err != nil {
		panic(err)
	}

	// Expressions within the concatenated string may be Get expressions and must
	// be resolved while concatenating.
	result, specErr := eval.NewEvaluator(tc.loader.(DefiningLoader), NewStdLogger()).Evaluate(ex, tc.newLazyScope(), tc.loader)
	if specErr != nil {
		panic(specErr)
	}
	return result
}

func (ls *LazyScope) Get(name string) (value PValue, found bool) {
	tc := ls.ctx
	if lv, ok := tc.getLazyValue(name); ok {
		return tc.Get(lv), true
	}
	return ls.BasicScope.Get(name)
}

func makeDirectories(parent string, hash *HashValue) {
	for _, e := range hash.EntriesSlice() {
		name := e.Key().String()
		path := filepath.Join(parent, name)
		value := e.Value()
		if dir, ok := value.(*HashValue); ok {
			err := os.Mkdir(path, 0755)
			if err != nil {
				panic(err)
			}
			makeDirectories(path, dir)
		} else {
			writeFileValue(path, value)
		}
	}
}

func writeFileValue(path string, value PValue) {
	var err error
	switch value.(type) {
	case *StringValue:
		err = ioutil.WriteFile(path, []byte(value.String()), 0644)
	case *BinaryValue:
		err = ioutil.WriteFile(path, value.(*BinaryValue).Bytes(), 0644)
	default:
		panic(Error(PSPEC_INVALID_FILE_CONTENT, issue.H{`value`: value}))
	}
	if err != nil {
		panic(err)
	}
}

func init() {
	NewGoConstructor(`PSpec::Directory`,
		func(d Dispatch) {
			d.Param(`Any`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				return WrapRuntime(newDirectoryValue(args[0]))
			})
		})

	NewGoConstructor(`PSpec::File`,
		func(d Dispatch) {
			d.Param(`Any`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				return WrapRuntime(newFileValue(args[0]))
			})
		})

	NewGoConstructor(`PSpec::Quote`,
		func(d Dispatch) {
			d.Param(`Any`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				return WrapRuntime(newQuoteValue(args[0]))
			})
		})

	NewGoConstructor(`PSpec::Get`,
		func(d Dispatch) {
			d.Param(`String[1]`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				return WrapRuntime(&LazyValueGet{args[0].String()})
			})
		})

	NewGoConstructor(`PSpec::Let`,
		func(d Dispatch) {
			d.Param(`String[1]`)
			d.Param(`Any`)
			d.Function(func(c EvalContext, args []PValue) PValue {
				v := args[1]
				var lv LazyValue
				r, ok := v.(*RuntimeValue)
				if ok {
					lv, ok = r.Interface().(LazyValue)
				}
				if !ok {
					lv = newGenericValue(v)
				}
				return WrapRuntime(&LazyValueLet{args[0].String(), lv})
			})
		})
}
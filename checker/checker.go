package checker

import (
	"fmt"
	"reflect"

	"github.com/antonmedv/expr/ast"
	"github.com/antonmedv/expr/conf"
	"github.com/antonmedv/expr/file"
	"github.com/antonmedv/expr/parser"
)

func Check(tree *parser.Tree, config *conf.Config) (reflect.Type, error) {
	v := &visitor{
		config:      config,
		collections: make([]reflect.Type, 0),
	}

	t, _ := v.visit(tree.Node)

	if v.err != nil {
		return t, v.err.Bind(tree.Source)
	}

	if v.config.Expect != reflect.Invalid {
		switch v.config.Expect {
		case reflect.Int64, reflect.Float64:
			if !isNumber(t) {
				return nil, fmt.Errorf("expected %v, but got %v", v.config.Expect, t)
			}
		default:
			if t == nil || t.Kind() != v.config.Expect {
				return nil, fmt.Errorf("expected %v, but got %v", v.config.Expect, t)
			}
		}
	}

	return t, nil
}

type visitor struct {
	config      *conf.Config
	collections []reflect.Type
	err         *file.Error
}

type info struct {
	method bool
}

func (v *visitor) visit(node ast.Node) (reflect.Type, info) {
	var t reflect.Type
	var i info
	switch n := node.(type) {
	case *ast.NilNode:
		t, i = v.NilNode(n)
	case *ast.IdentifierNode:
		t, i = v.IdentifierNode(n)
	case *ast.IntegerNode:
		t, i = v.IntegerNode(n)
	case *ast.FloatNode:
		t, i = v.FloatNode(n)
	case *ast.BoolNode:
		t, i = v.BoolNode(n)
	case *ast.StringNode:
		t, i = v.StringNode(n)
	case *ast.ConstantNode:
		t, i = v.ConstantNode(n)
	case *ast.UnaryNode:
		t, i = v.UnaryNode(n)
	case *ast.BinaryNode:
		t, i = v.BinaryNode(n)
	case *ast.MatchesNode:
		t, i = v.MatchesNode(n)
	case *ast.MemberNode:
		t, i = v.MemberNode(n)
	case *ast.SliceNode:
		t, i = v.SliceNode(n)
	case *ast.CallNode:
		t, i = v.CallNode(n)
	case *ast.BuiltinNode:
		t, i = v.BuiltinNode(n)
	case *ast.ClosureNode:
		t, i = v.ClosureNode(n)
	case *ast.PointerNode:
		t, i = v.PointerNode(n)
	case *ast.ConditionalNode:
		t, i = v.ConditionalNode(n)
	case *ast.ArrayNode:
		t, i = v.ArrayNode(n)
	case *ast.MapNode:
		t, i = v.MapNode(n)
	case *ast.PairNode:
		t, i = v.PairNode(n)
	default:
		panic(fmt.Sprintf("undefined node type (%T)", node))
	}
	node.SetType(t)
	return t, i
}

func (v *visitor) error(node ast.Node, format string, args ...interface{}) (reflect.Type, info) {
	if v.err == nil { // show first error
		v.err = &file.Error{
			Location: node.Location(),
			Message:  fmt.Sprintf(format, args...),
		}
	}
	return interfaceType, info{} // interface represent undefined type
}

func (v *visitor) NilNode(*ast.NilNode) (reflect.Type, info) {
	return nilType, info{}
}

func (v *visitor) IdentifierNode(node *ast.IdentifierNode) (reflect.Type, info) {
	if v.config.Types == nil {
		return interfaceType, info{}
	}
	if t, ok := v.config.Types[node.Value]; ok {
		if t.Ambiguous {
			return v.error(node, "ambiguous identifier %v", node.Value)
		}
		return t.Type, info{method: t.Method}
	}
	if !v.config.Strict {
		if v.config.DefaultType != nil {
			return v.config.DefaultType, info{}
		}
		return interfaceType, info{}
	}
	return v.error(node, "unknown name %v", node.Value)
}

func (v *visitor) IntegerNode(*ast.IntegerNode) (reflect.Type, info) {
	return integerType, info{}
}

func (v *visitor) FloatNode(*ast.FloatNode) (reflect.Type, info) {
	return floatType, info{}
}

func (v *visitor) BoolNode(*ast.BoolNode) (reflect.Type, info) {
	return boolType, info{}
}

func (v *visitor) StringNode(*ast.StringNode) (reflect.Type, info) {
	return stringType, info{}
}

func (v *visitor) ConstantNode(node *ast.ConstantNode) (reflect.Type, info) {
	return reflect.TypeOf(node.Value), info{}
}

func (v *visitor) UnaryNode(node *ast.UnaryNode) (reflect.Type, info) {
	t, _ := v.visit(node.Node)

	switch node.Operator {

	case "!", "not":
		if isBool(t) {
			return boolType, info{}
		}

	case "+", "-":
		if isNumber(t) {
			return t, info{}
		}

	default:
		return v.error(node, "unknown operator (%v)", node.Operator)
	}

	return v.error(node, `invalid operation: %v (mismatched type %v)`, node.Operator, t)
}

func (v *visitor) BinaryNode(node *ast.BinaryNode) (reflect.Type, info) {
	l, _ := v.visit(node.Left)
	r, _ := v.visit(node.Right)

	// check operator overloading
	if fns, ok := v.config.Operators[node.Operator]; ok {
		t, _, ok := conf.FindSuitableOperatorOverload(fns, v.config.Types, l, r)
		if ok {
			return t, info{}
		}
	}

	switch node.Operator {
	case "==", "!=":
		if isNumber(l) && isNumber(r) {
			return boolType, info{}
		}
		if isComparable(l, r) {
			return boolType, info{}
		}

	case "or", "||", "and", "&&":
		if isBool(l) && isBool(r) {
			return boolType, info{}
		}

	case "in", "not in":
		if isString(l) && isStruct(r) {
			return boolType, info{}
		}
		if isMap(r) {
			return boolType, info{}
		}
		if isArray(r) {
			return boolType, info{}
		}

	case "<", ">", ">=", "<=":
		if isNumber(l) && isNumber(r) {
			return boolType, info{}
		}
		if isString(l) && isString(r) {
			return boolType, info{}
		}
		if isTime(l) && isTime(r) {
			return boolType, info{}
		}

	case "-":
		if isNumber(l) && isNumber(r) {
			return combined(l, r), info{}
		}
		if isTime(l) && isTime(r) {
			return durationType, info{}
		}

	case "/", "*":
		if isNumber(l) && isNumber(r) {
			return combined(l, r), info{}
		}

	case "**":
		if isNumber(l) && isNumber(r) {
			return floatType, info{}
		}

	case "%":
		if isInteger(l) && isInteger(r) {
			return combined(l, r), info{}
		}

	case "+":
		if isNumber(l) && isNumber(r) {
			return combined(l, r), info{}
		}
		if isString(l) && isString(r) {
			return stringType, info{}
		}
		if isTime(l) && isDuration(r) {
			return timeType, info{}
		}
		if isDuration(l) && isTime(r) {
			return timeType, info{}
		}

	case "contains", "startsWith", "endsWith":
		if isString(l) && isString(r) {
			return boolType, info{}
		}

	case "..":
		if isInteger(l) && isInteger(r) {
			return reflect.SliceOf(integerType), info{}
		}

	default:
		return v.error(node, "unknown operator (%v)", node.Operator)

	}

	return v.error(node, `invalid operation: %v (mismatched types %v and %v)`, node.Operator, l, r)
}

func (v *visitor) MatchesNode(node *ast.MatchesNode) (reflect.Type, info) {
	l, _ := v.visit(node.Left)
	r, _ := v.visit(node.Right)

	if isString(l) && isString(r) {
		return boolType, info{}
	}

	return v.error(node, `invalid operation: matches (mismatched types %v and %v)`, l, r)
}

func (v *visitor) MemberNode(node *ast.MemberNode) (reflect.Type, info) {
	base, _ := v.visit(node.Node)
	prop, _ := v.visit(node.Property)

	if name, ok := node.Property.(*ast.StringNode); ok {
		// First, check methods defined on base type itself,
		// independent of which type it is. Without dereferencing.
		if m, ok := base.MethodByName(name.Value); ok {
			if base.Kind() == reflect.Interface {
				// In case of interface type method will not have a receiver,
				// and to prevent checker decreasing numbers of in arguments
				// return method type as not method (second argument is false).
				return m.Type, info{}
			} else {
				return m.Type, info{method: true}
			}
		}
	}

	switch base.Kind() {
	case reflect.Interface:
		return interfaceType, info{}

	case reflect.Map:
		// TODO: check key type == prop
		return base.Elem(), info{}

	case reflect.Array, reflect.Slice:
		if !isInteger(prop) {
			return v.error(node.Property, "invalid operation: cannot use %v as index to %v", prop, base)
		}
		return base.Elem(), info{}

	case reflect.Struct:

	}
	switch prop.Kind() {
	case reflect.String:
		if name, ok := node.Property.(*ast.StringNode); ok {
			if t, ok := fetchType(base, name.Value); ok {
				return t, info{}
			}
		}
	}
	return v.error(node, "type %v has no field %v", base, node.Property)
}

func (v *visitor) SliceNode(node *ast.SliceNode) (reflect.Type, info) {
	t, _ := v.visit(node.Node)

	isIndex := true // TODO: check if it is index or slice

	if isIndex || isString(t) {
		if node.From != nil {
			from, _ := v.visit(node.From)
			if !isInteger(from) {
				return v.error(node.From, "invalid operation: non-integer slice index %v", from)
			}
		}
		if node.To != nil {
			to, _ := v.visit(node.To)
			if !isInteger(to) {
				return v.error(node.To, "invalid operation: non-integer slice index %v", to)
			}
		}
		return t, info{}
	}

	return v.error(node, "invalid operation: cannot slice %v", t)
}

func (v *visitor) CallNode(node *ast.CallNode) (reflect.Type, info) {
	fn, fnInfo := v.visit(node.Callee)

	switch fn.Kind() {
	case reflect.Interface:
		return interfaceType, info{}
	case reflect.Func:
		inputParamsCount := 1 // for functions
		if fnInfo.method {
			inputParamsCount = 2 // for methods
		}

		if !isInterface(fn) &&
			fn.IsVariadic() &&
			fn.NumIn() == inputParamsCount &&
			((fn.NumOut() == 1 && // Function with one return value
				fn.Out(0).Kind() == reflect.Interface) ||
				(fn.NumOut() == 2 && // Function with one return value and an error
					fn.Out(0).Kind() == reflect.Interface &&
					fn.Out(1) == errorType)) {
			rest := fn.In(fn.NumIn() - 1) // function has only one param for functions and two for methods
			if rest.Kind() == reflect.Slice && rest.Elem().Kind() == reflect.Interface {
				node.Fast = true
			}
		}

		return v.checkFunc(fn, fnInfo.method, node, "node.Name", node.Arguments)
	}
	return v.error(node, "unknown func %v", "node.Name")
}

// checkFunc checks func arguments and returns "return type" of func or method.
func (v *visitor) checkFunc(fn reflect.Type, method bool, node ast.Node, name string, arguments []ast.Node) (reflect.Type, info) {
	if isInterface(fn) {
		return interfaceType, info{}
	}

	if fn.NumOut() == 0 {
		return v.error(node, "func %v doesn't return value", name)
	}
	if numOut := fn.NumOut(); numOut > 2 {
		return v.error(node, "func %v returns more then two values", name)
	}

	numIn := fn.NumIn()

	// If func is method on an env, first argument should be a receiver,
	// and actual arguments less than numIn by one.
	if method {
		numIn--
	}

	if fn.IsVariadic() {
		if len(arguments) < numIn-1 {
			return v.error(node, "not enough arguments to call %v", name)
		}
	} else {
		if len(arguments) > numIn {
			return v.error(node, "too many arguments to call %v", name)
		}
		if len(arguments) < numIn {
			return v.error(node, "not enough arguments to call %v", name)
		}
	}

	offset := 0

	// Skip first argument in case of the receiver.
	if method {
		offset = 1
	}

	for i, arg := range arguments {
		t, _ := v.visit(arg)

		var in reflect.Type
		if fn.IsVariadic() && i >= numIn-1 {
			// For variadic arguments fn(xs ...int), go replaces type of xs (int) with ([]int).
			// As we compare arguments one by one, we need underling type.
			in = fn.In(fn.NumIn() - 1).Elem()
		} else {
			in = fn.In(i + offset)
		}

		if isIntegerOrArithmeticOperation(arg) {
			t = in
			setTypeForIntegers(arg, t)
		}

		if t == nil {
			continue
		}

		if !t.AssignableTo(in) && t.Kind() != reflect.Interface {
			return v.error(arg, "cannot use %v as argument (type %v) to call %v ", t, in, name)
		}
	}

	return fn.Out(0), info{}
}

func (v *visitor) BuiltinNode(node *ast.BuiltinNode) (reflect.Type, info) {
	switch node.Name {

	case "len":
		param, _ := v.visit(node.Arguments[0])
		if isArray(param) || isMap(param) || isString(param) {
			return integerType, info{}
		}
		return v.error(node, "invalid argument for len (type %v)", param)

	case "all", "none", "any", "one":
		collection, _ := v.visit(node.Arguments[0])
		if !isArray(collection) {
			return v.error(node.Arguments[0], "builtin %v takes only array (got %v)", node.Name, collection)
		}

		v.collections = append(v.collections, collection)
		closure, _ := v.visit(node.Arguments[1])
		v.collections = v.collections[:len(v.collections)-1]

		if isFunc(closure) &&
			closure.NumOut() == 1 &&
			closure.NumIn() == 1 && isInterface(closure.In(0)) {

			if !isBool(closure.Out(0)) {
				return v.error(node.Arguments[1], "closure should return boolean (got %v)", closure.Out(0).String())
			}
			return boolType, info{}
		}
		return v.error(node.Arguments[1], "closure should has one input and one output param")

	case "filter":
		collection, _ := v.visit(node.Arguments[0])
		if !isArray(collection) {
			return v.error(node.Arguments[0], "builtin %v takes only array (got %v)", node.Name, collection)
		}

		v.collections = append(v.collections, collection)
		closure, _ := v.visit(node.Arguments[1])
		v.collections = v.collections[:len(v.collections)-1]

		if isFunc(closure) &&
			closure.NumOut() == 1 &&
			closure.NumIn() == 1 && isInterface(closure.In(0)) {

			if !isBool(closure.Out(0)) {
				return v.error(node.Arguments[1], "closure should return boolean (got %v)", closure.Out(0).String())
			}
			if isInterface(collection) {
				return arrayType, info{}
			}
			return reflect.SliceOf(collection.Elem()), info{}
		}
		return v.error(node.Arguments[1], "closure should has one input and one output param")

	case "map":
		collection, _ := v.visit(node.Arguments[0])
		if !isArray(collection) {
			return v.error(node.Arguments[0], "builtin %v takes only array (got %v)", node.Name, collection)
		}

		v.collections = append(v.collections, collection)
		closure, _ := v.visit(node.Arguments[1])
		v.collections = v.collections[:len(v.collections)-1]

		if isFunc(closure) &&
			closure.NumOut() == 1 &&
			closure.NumIn() == 1 && isInterface(closure.In(0)) {

			return reflect.SliceOf(closure.Out(0)), info{}
		}
		return v.error(node.Arguments[1], "closure should has one input and one output param")

	case "count":
		collection, _ := v.visit(node.Arguments[0])
		if !isArray(collection) {
			return v.error(node.Arguments[0], "builtin %v takes only array (got %v)", node.Name, collection)
		}

		v.collections = append(v.collections, collection)
		closure, _ := v.visit(node.Arguments[1])
		v.collections = v.collections[:len(v.collections)-1]

		if isFunc(closure) &&
			closure.NumOut() == 1 &&
			closure.NumIn() == 1 && isInterface(closure.In(0)) {
			if !isBool(closure.Out(0)) {
				return v.error(node.Arguments[1], "closure should return boolean (got %v)", closure.Out(0).String())
			}

			return integerType, info{}
		}
		return v.error(node.Arguments[1], "closure should has one input and one output param")

	default:
		return v.error(node, "unknown builtin %v", node.Name)
	}
}

func (v *visitor) ClosureNode(node *ast.ClosureNode) (reflect.Type, info) {
	t, _ := v.visit(node.Node)
	return reflect.FuncOf([]reflect.Type{interfaceType}, []reflect.Type{t}, false), info{}
}

func (v *visitor) PointerNode(node *ast.PointerNode) (reflect.Type, info) {
	if len(v.collections) == 0 {
		return v.error(node, "cannot use pointer accessor outside closure")
	}

	collection := v.collections[len(v.collections)-1]
	switch collection.Kind() {
	case reflect.Array, reflect.Slice:
		return collection.Elem(), info{}
	}
	return v.error(node, "cannot use %v as array", collection)
}

func (v *visitor) ConditionalNode(node *ast.ConditionalNode) (reflect.Type, info) {
	c, _ := v.visit(node.Cond)
	if !isBool(c) {
		return v.error(node.Cond, "non-bool expression (type %v) used as condition", c)
	}

	t1, _ := v.visit(node.Exp1)
	t2, _ := v.visit(node.Exp2)

	if t1 == nil && t2 != nil {
		return t2, info{}
	}
	if t1 != nil && t2 == nil {
		return t1, info{}
	}
	if t1 == nil && t2 == nil {
		return nilType, info{}
	}
	if t1.AssignableTo(t2) {
		return t1, info{}
	}
	return interfaceType, info{}
}

func (v *visitor) ArrayNode(node *ast.ArrayNode) (reflect.Type, info) {
	for _, node := range node.Nodes {
		v.visit(node)
	}
	return arrayType, info{}
}

func (v *visitor) MapNode(node *ast.MapNode) (reflect.Type, info) {
	for _, pair := range node.Pairs {
		v.visit(pair)
	}
	return mapType, info{}
}

func (v *visitor) PairNode(node *ast.PairNode) (reflect.Type, info) {
	v.visit(node.Key)
	v.visit(node.Value)
	return nilType, info{}
}

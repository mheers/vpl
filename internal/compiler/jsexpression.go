package compiler

import (
	"encoding/json"
	"fmt"
	"github.com/robertkrimen/otto/ast"
	"github.com/robertkrimen/otto/parser"
	"github.com/robertkrimen/otto/token"
	"html"
	"strconv"
)

func compileJS(code string) (node ast.Node, err error) {
	// 用括号包裹的原因是让"{x: 1}"这样的语法解析成对象, 而不是label
	code = "(" + code + ")"
	p, err := parser.ParseFile(nil, "", code, 0)
	if err != nil {
		err = fmt.Errorf("ParseFile err: %w, code:%s", err, code)
		return nil, err
	}

	return p.Body[0], nil
}

func runJsExpression(node ast.Node, scope *Scope) (r interface{}, err error) {
	switch t := node.(type) {
	case *ast.ExpressionStatement:
		return runJsExpression(t.Expression, scope)
	case *ast.Identifier:
		return scope.Get(t.Name), nil
	case *ast.DotExpression:
		// a.b
		left, err := runJsExpression(t.Left, scope)
		if err != nil {
			return nil, err
		}

		r, _, _ := shouldLookInterface(left, t.Identifier.Name)
		return r, nil
	case *ast.BracketExpression:
		// a[b]
		left, err := runJsExpression(t.Left, scope)
		if err != nil {
			return nil, err
		}
		var key string
		switch m := t.Member.(type) {
		case *ast.StringLiteral:
			// a['b']
			// 也可以走default语句, 但这是fastPath, 可以少调用interfaceToStr函数
			key = m.Value
		default:
			// a[b+c]
			v, err := runJsExpression(t.Member, scope)
			if err != nil {
				return nil, err
			}

			key = interfaceToStr(v)
		}

		r, _, _ := shouldLookInterface(left, key)
		return r, nil
	case *ast.StringLiteral:
		return t.Value, nil
	case *ast.NumberLiteral:
		return t.Value, nil
	case *ast.BooleanLiteral:
		return t.Value, nil
	case *ast.NullLiteral:
		return nil, nil
	case *ast.BinaryExpression:
		left, err := runJsExpression(t.Left, scope)
		if err != nil {
			return nil, err
		}
		right, err := runJsExpression(t.Right, scope)
		if err != nil {
			return nil, err
		}
		o := t.Operator
		switch o {
		case token.STRICT_EQUAL, token.EQUAL:
			return left == right, nil
		case token.NOT_EQUAL, token.STRICT_NOT_EQUAL:
			return left != right, nil
		case token.PLUS:
			return interfaceAdd(left, right), nil
		case token.MINUS:
			return interfaceToFloat(left) - interfaceToFloat(right), nil
		case token.MULTIPLY:
			return interfaceToFloat(left) * interfaceToFloat(right), nil
		case token.SLASH:
			return interfaceToFloat(left) / interfaceToFloat(right), nil
		case token.LOGICAL_AND:
			return interfaceToBool(left) && interfaceToBool(right), nil
		case token.LOGICAL_OR:
			return interfaceToBool(left) || interfaceToBool(right), nil
		case token.LESS:
			return interfaceLess(left, right), nil
		case token.GREATER:
			return interfaceGreater(left, right), nil

		default:
			panic(fmt.Sprintf("bad Operator for BinaryExpression: %s", o))
		}

	case *ast.UnaryExpression:
		// 一元运算符
		// -1
		// !b
		arg, err := runJsExpression(t.Operand, scope)
		if err != nil {
			return nil, err
		}
		switch t.Operator {
		case token.NOT:
			return !interfaceToBool(arg), nil
		case token.MINUS:
			// -1
			return -interfaceToFloat(arg), nil
		default:
			panic(fmt.Sprintf("not handle UnaryExpression: %s", t.Operator))
		}
	case *ast.ObjectLiteral:
		if len(t.Value) == 0 {
			return nil, nil
		}

		// 对象, 翻译成map[string]interface{}
		mp := map[string]interface{}{}
		for _, v := range t.Value {
			k := ""

			switch v.Kind {
			case "value":
				k = v.Key
			default:
				panic(fmt.Sprintf("bad Value kind of ObjectLiteral: %v", v.Kind))
			}

			val, err := runJsExpression(v.Value, scope)
			if err != nil {
				return nil, err
			}
			mp[k] = val
		}
		return mp, nil
	case *ast.CallExpression:
		// fun(1,2,3, ...)
		funcName, err := runJsExpression(t.Callee, scope)
		if err != nil {
			return nil, err
		}

		args := make([]interface{}, len(t.ArgumentList))
		for i, v := range t.ArgumentList {
			args[i], err = runJsExpression(v, scope)
			if err != nil {
				return nil, err
			}
		}
		return interfaceToFunc(funcName)(args...), nil
	case *ast.ArrayLiteral:
		args := make([]interface{}, len(t.Value))
		for i, v := range t.Value {
			args[i], err = runJsExpression(v, scope)
			if err != nil {
				return nil, err
			}
		}
		return args, nil
	case *ast.ConditionalExpression:
		// 三元运算
		consequent, err := runJsExpression(t.Consequent, scope)
		if err != nil {
			return nil, err
		}
		alternate, err := runJsExpression(t.Alternate, scope)
		if err != nil {
			return nil, err
		}
		test, err := runJsExpression(t.Test, scope)
		if err != nil {
			return nil, err
		}

		if interfaceToBool(test) {
			return consequent, err
		} else {
			return alternate, err
		}

	default:
		panic(fmt.Sprintf("bad type %T for runJsExpression", t))
		//bs, _ := json.Marshal(t)
		//log.Panicf("%s", bs)
	}
	return
}

// 读取值
// 将a.b.c解析成 root 和keys
// 如a.b.c解析成 root: this, keys: [a ,b ,c]
// 如"a".length解析成 root: "a", keys: [length]
//func lookExpress(e ast.Expression, scopeKey string) (root string, keys []string) {
//	switch r := e.(type) {
//	case *ast.DotExpression:
//		// a.b 中的b
//		currKey := fmt.Sprintf(`"%s"`, r.Identifier.Name)
//		root, keys = lookExpress(r.Left, scopeKey)
//		keys = append(keys, currKey)
//	case *ast.Identifier:
//		// a.b 中的a
//		// 使用scopeKey读取变量
//		root = scopeKey
//		keys = []string{fmt.Sprintf(`"%s"`, r.Name)}
//	case *ast.ObjectLiteral:
//		// {"a": 1}["a"]
//		root = genGoCodeByNode(r, scopeKey)
//	case *ast.BinaryExpression:
//		root = genGoCodeByNode(r, scopeKey)
//	case *ast.BracketExpression:
//		var currKey string
//		switch m := r.Member.(type) {
//		case *ast.StringLiteral:
//			// a['b']
//			// 也可以走default语句, 但这是fastPath, 可以少调用interfaceToStr函数
//			currKey = fmt.Sprintf(`"%s"`, m.Value)
//		default:
//			// a[b]
//			// a[a+1]
//			// ... 各种表达式
//			currKey = fmt.Sprintf(`interfaceToStr(%s)`, genGoCodeByNode(r.Member, scopeKey))
//		}
//
//		root, keys = lookExpress(r.Left, scopeKey)
//		keys = append(keys, currKey)
//	default:
//		panic(fmt.Sprintf("bad type for lookExpress: %T, %s", r, r))
//	}
//
//	return
//}

func isNumber(s interface{}) (d float64, is bool) {
	if s == nil {
		return 0, false
	}
	switch a := s.(type) {
	case int:
		return float64(a), true
	case int32:
		return float64(a), true
	case int64:
		return float64(a), true
	case float64:
		return a, true
	case float32:
		return float64(a), true
	default:
		return 0, false
	}
}

// 用来模拟js两个变量相加
// 如果两个变量都是number, 则相加后也是number
// 只有有一个不是number, 则都按字符串处理相加
func interfaceAdd(a, b interface{}) interface{} {
	an, ok := isNumber(a)
	if !ok {
		return interfaceToStr(a) + interfaceToStr(b)
	}
	bn, ok := isNumber(b)
	if !ok {
		return interfaceToStr(a) + interfaceToStr(b)
	}

	return an + bn
}

func interfaceToStr(s interface{}, escaped ...bool) (d string) {
	switch a := s.(type) {
	case string:
		d = a
	case int:
		d = strconv.FormatInt(int64(a), 10)
	case int32:
		d = strconv.FormatInt(int64(a), 10)
	case int64:
		d = strconv.FormatInt(a, 10)
	case float64:
		d = strconv.FormatFloat(a, 'f', 10, 64)
	default:
		bs, _ := json.Marshal(a)
		d = string(bs)
	}

	if len(escaped) == 1 && escaped[0] {
		d = escape(d)
	}
	return
}

func escape(src string) string {
	return html.EscapeString(src)
}

// 字符串false,0 会被认定为false
func interfaceToBool(s interface{}) (d bool) {
	if s == nil {
		return false
	}
	switch a := s.(type) {
	case bool:
		return a
	case int:
		return a != 0
	case int8:
		return a != 0
	case int16:
		return a != 0
	case int32:
		return a != 0
	case int64:
		return a != 0
	case float64:
		return a != 0
	case float32:
		return a != 0
	case string:
		return a != "" && a != "false" && a != "0"
	default:
		return true
	}
}

func interfaceToFloat(s interface{}) (d float64) {
	if s == nil {
		return 0
	}
	switch a := s.(type) {
	case int:
		return float64(a)
	case int8:
		return float64(a)
	case int16:
		return float64(a)
	case int32:
		return float64(a)
	case int64:
		return float64(a)
	case float32:
		return float64(a)
	case float64:
		return a
	default:
		return 0
	}
}

func interfaceLess(a, b interface{}) interface{} {
	an, ok := isNumber(a)
	if !ok {
		return interfaceToStr(a) < interfaceToStr(b)
	}
	bn, ok := isNumber(b)
	if !ok {
		return interfaceToStr(a) < interfaceToStr(b)
	}

	return an < bn
}

func interfaceGreater(a, b interface{}) interface{} {
	an, ok := isNumber(a)
	if !ok {
		return interfaceToStr(a) > interfaceToStr(b)
	}
	bn, ok := isNumber(b)
	if !ok {
		return interfaceToStr(a) > interfaceToStr(b)
	}

	return an > bn
}

// 用于{{func(a)}}语法
func interfaceToFunc(s interface{}) (d Function) {
	if s == nil {
		return emptyFunc
	}

	switch a := s.(type) {
	case func(args ...interface{}) interface{}:
		return a
	case Function:
		return a
	default:
		panic(a)
		return emptyFunc
	}
}

type Function func(args ...interface{}) interface{}

func emptyFunc( args ...interface{}) interface{} {
	if len(args) != 0 {
		return args[0]
	}
	return nil
}


// shouldLookInterface会返回interface(map[string]interface{})中指定的keys路径的值
func shouldLookInterface(data interface{}, keys ...string) (desc interface{}, rootExist bool, exist bool) {
	if len(keys) == 0 {
		return data, true, true
	}

	currKey := keys[0]

	switch data := data.(type) {
	case map[string]interface{}:
		// 对象
		c, ok := data[currKey]
		if !ok {
			return
		}
		rootExist = true
		desc, _, exist = shouldLookInterface(c, keys[1:]...)
		return

	case []interface{}:
		// 数组
		switch currKey {
		case "length":
			// length
			return len(data), true, true
		default:
			// index
			index, ok := strconv.ParseInt(currKey, 10, 64)
			if ok != nil {
				return
			}

			if int(index) >= len(data) || index < 0 {
				return
			}
			return shouldLookInterface(data[index], keys[1:]...)
		}
	case string:
		switch currKey {
		case "length":
			// length
			return len(data), true, true
		default:
		}
	}

	return
}

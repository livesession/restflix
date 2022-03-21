package restlix

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/structtag"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3gen"
	"github.com/kataras/iris/v12/core/router"
	dynamicstruct "github.com/ompluscator/dynamic-struct"
	gogitignore "github.com/sabhiram/go-gitignore"
)

type SearchIdentifier struct {
	MethodStatement  []string
	ArgumentPosition int
}

func parseRouterMethod(
	openapi *openapi3.T,
	sourceFileName string,
	findMethodStatement string,
	route *router.Route,
	structsMapping map[string]map[string]*ast.TypeSpec,
	searchIdentifiers []*SearchIdentifier,
) {
	if route.Method == http.MethodOptions {
		return
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, sourceFileName, nil, parser.ParseComments) // 1. parse AST for router source file
	if err != nil {
		log.Fatal(err)
	}

	bodyParsed := false
	responseParsed := false

	operationName := fmt.Sprintf("[%s]%s", route.Method, strings.ReplaceAll(route.Path, "/", "-"))

	if operationName == debugOperationMethod {
		fmt.Sprintf("d")
	}

	// path
	var item *openapi3.PathItem

	if _, ok := openapi.Paths[route.Path]; ok {
		item = openapi.Paths[route.Path]
	} else {
		item = &openapi3.PathItem{}
	}

	pathOperation := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Ref: "#/components/requestBodies/" + operationName,
		},
		// TODO: 200, 400, 404, 500
		Responses: openapi3.NewResponses(),
	}

	switch route.Method {
	case http.MethodGet:
		item.Get = pathOperation
	case http.MethodPost:
		item.Post = pathOperation
	case http.MethodPut:
		item.Put = pathOperation
	case http.MethodPatch:
		item.Patch = pathOperation
	case http.MethodDelete:
		item.Delete = pathOperation
	}

	openapi.Paths[route.Path] = item

root:
	for _, f := range node.Decls {
		fn, ok := f.(*ast.FuncDecl) // 2. check functions/methods only  // TODO: check if works with router inside method not function
		if !ok {
			continue
		}

		methodName := fn.Name.Name

		if fn.Recv == nil || len(fn.Recv.List) <= 0 {
			continue
		}

		// 3. find router method e.g func (a *api) updateUser(ctx iris.Context)
		for _, recv := range fn.Recv.List { // 4. list pointers
			// TODO: support pointers and non pointers
			if star, ok := recv.Type.(*ast.StarExpr); ok { // 5. check if pointer e.g *api
				if _, ok := star.X.(*ast.Ident); ok { // 6. get pointer name e.g "api"
					routerMethodMatchWithASTMethod := methodName == findMethodStatement // 7. check if ast method match with iris router method

					if operationName == debugOperationMethod {
						fmt.Sprintf("d")
					}

					if !routerMethodMatchWithASTMethod {
						continue
					}

					if operationName == debugOperationMethod {
						fmt.Sprintf("d")
					}

					if fn.Body != nil && len(fn.Body.List) > 0 { // 8. check body - there should be request validation
						for _, body := range fn.Body.List {
							if bodyParsed && responseParsed {
								break root
							}
							switch t := body.(type) {
							case *ast.AssignStmt: // 9. maybe validation in assign statement
								if t.Rhs == nil {
									continue
								}

								for _, rhs := range t.Rhs {
									if operationName == debugOperationMethod {
										fmt.Sprintf("d")
									}
									if parseHandlerBodyAST(node, sourceFileName, structsMapping, operationName, searchIdentifiers, rhs, openapi, route) {
										bodyParsed = true
									}
								}
							case *ast.IfStmt: // 10. or maybe it's in if statement first
								if t.Init == nil {
									continue
								}

								init, ok := t.Init.(*ast.AssignStmt) // 11. and then check assign (inside if)
								if !ok || init.Rhs == nil {
									continue
								}

								for _, rhs := range init.Rhs {
									if operationName == debugOperationMethod {
										fmt.Sprintf("d")
									}
									if parseHandlerBodyAST(node, sourceFileName, structsMapping, operationName, searchIdentifiers, rhs, openapi, route) {
										bodyParsed = true
									}
								}

								// parse body
							case *ast.ExprStmt: // TODO: response body, support 400, 404, 500 etc.
								// TODO: response with responses structures in different packages and files
								parseContextJSONStruct := func(compositeList *ast.CompositeLit) {
									compositeTypIdent, ok := compositeList.Type.(*ast.Ident)
									if !ok {
										return
									}

									typeSpec, ok := compositeTypIdent.Obj.Decl.(*ast.TypeSpec)
									if !ok {
										return
									}

									responseBodyStruct := dynamicstruct.NewStruct()

									if err := parseStruct(typeSpec, responseBodyStruct); err != nil {
										log.Println(err)
										return
									}

									requestBodyStructInstance := responseBodyStruct.Build().New()

									schemaRef, _, err := openapi3gen.NewSchemaRefForValue(requestBodyStructInstance)
									if err != nil {
										log.Println(err)
										return
									}
									responseJSON := openapi3.NewResponse().
										WithJSONSchemaRef(schemaRef)

									// TOOD: support 400, 404, 500 etc.
									//openapi.Components.Responses[operationName] = &openapi3.ResponseRef{
									//	Value: responseJSON,
									//} // TODO:
									operation := openAPIOperationByMethod(openapi.Paths[route.Path], route.Method)
									operation.Responses.Default().Value = responseJSON

									responseParsed = true
									return
								}

								call, ok := t.X.(*ast.CallExpr)
								if !ok {
									continue
								}

								selector, ok := call.Fun.(*ast.SelectorExpr)
								if !ok {
									continue
								}

								if selector.Sel.Name != "JSON" {
									continue
								}

								ident, ok := selector.X.(*ast.Ident)
								if !ok {
									continue
								}

								f, ok := ident.Obj.Decl.(*ast.Field)
								if !ok {
									continue
								}

								tx, ok := f.Type.(*ast.SelectorExpr)
								if !ok {
									continue
								}

								if c, ok := tx.X.(*ast.Ident); !ok {
									continue
								} else if c.Name != "iris" {
									continue
								}

								if tx.Sel.Name != "Context" {
									continue
								}

								// yes it's iris context.JSON()

								if len(call.Args) < 1 {
									continue
								}

								argIdent, ok := call.Args[0].(*ast.Ident) // ctx.JSON(resp)
								if !ok {
									if compositeList, ok := call.Args[0].(*ast.CompositeLit); ok { // if ctx.JSON(&Struct{})
										parseContextJSONStruct(compositeList)
									}
									continue
								}

								// else ctx.JSON(&variable)

								assignStmt, ok := argIdent.Obj.Decl.(*ast.AssignStmt)
								if !ok {
									continue
								}

								if len(assignStmt.Rhs) < 1 {
									continue
								}

								compositeList, ok := assignStmt.Rhs[0].(*ast.CompositeLit)
								if !ok {
									continue
								}

								parseContextJSONStruct(compositeList)
							}
						}
					}
					continue root
				}
			}
		}
	}
}

func parseHandlerBodyAST(
	node *ast.File,
	sourceFileName string,
	structsMapping map[string]map[string]*ast.TypeSpec,
	operationName string,
	searchIdentifiers []*SearchIdentifier,
	exp ast.Expr,
	openapi *openapi3.T,
	route *router.Route,
) bool {
	callExpresssions, argumentPosition, match := matchCallExpression(operationName, searchIdentifiers, exp) // 1. match call expression e.g a.baseController.ValidateBody(ctx, &req)
	if operationName == debugOperationMethod {
		fmt.Sprintf("d")
	}
	if !match {
		return false
	}

	if operationName == debugOperationMethod {
		fmt.Sprintf("d")
	}

	// TODO: support non pointer request struct also
	requestBodyArg := callExpresssions.Args[argumentPosition] // 2. get validation struct e.g a.baseController.ValidateBody(ctx, &req) || &req ctx.ValidateRequest(&req)

	requestBodyStruct := dynamicstruct.NewStruct()

	//
	parseRequestStructIdent := func(ident *ast.Ident) {
		// TODO: strategy for in another file + in another package
		if ident.Obj == nil {
			sourceFileName = strings.ReplaceAll(sourceFileName, "./", "")
			sourceDir, _ := filepath.Split(sourceFileName)

			// TODO: dir mapping instead of loop all files
			for mappingPath, structs := range structsMapping { // TODO: stategy for files outside current package
				if mappingPath == sourceFileName {
					continue
				}

				dir, _ := filepath.Split(mappingPath)

				withinInnerPackage := sourceDir == dir

				if withinInnerPackage {
					if typeSpec, ok := structs[ident.Name]; ok && typeSpec != nil {
						if err := parseStruct(typeSpec, requestBodyStruct); err != nil {
							log.Println(err)
							continue
						}
						break
					}
				}
			}

			return
		}

		if typeSpec, ok := ident.Obj.Decl.(*ast.TypeSpec); ok {
			if err := parseStruct(typeSpec, requestBodyStruct); err != nil {
				log.Println(err)
				return
			}
		}
	}

	parsed := false
	switch reqBody := requestBodyArg.(type) {
	case *ast.UnaryExpr: //  if var req request
		if operationName == debugOperationMethod {
			fmt.Sprintf("d")
		}
		if ident, ok := reqBody.X.(*ast.Ident); ok {
			if valueSpec, ok := ident.Obj.Decl.(*ast.ValueSpec); ok {
				if ident, ok := valueSpec.Type.(*ast.Ident); ok {
					parseRequestStructIdent(ident)
					parsed = true
				}
			}
		}
	case *ast.Ident: //  if req := &request{}
		if operationName == debugOperationMethod {
			fmt.Sprintf("d")
		}
		if assign, ok := reqBody.Obj.Decl.(*ast.AssignStmt); ok {
			if expr, ok := assign.Rhs[0].(*ast.UnaryExpr); ok {
				if composeList, ok := expr.X.(*ast.CompositeLit); ok {
					switch t := composeList.Type.(type) {
					case *ast.Ident: // &req{}
						parseRequestStructIdent(t)
						parsed = true
					case *ast.SelectorExpr: // &pkg.req{}
					root:
						for _, nodeImport := range node.Imports { // search struct inside imports
							importName := ""

							if nodeImport.Name != nil {
								importName = nodeImport.Name.Name
							}

							nodeImportPath, _ := strconv.Unquote(nodeImport.Path.Value)
							_, pkg := filepath.Split(nodeImportPath)

							if importName == "" {
								importName = pkg
							}

							ident, ok := t.X.(*ast.Ident)
							if !ok {
								continue
							}

							if importName != ident.Name {
								continue
							}

							// TODO: dir mapping instead of loop all files
							for mappingPath, structs := range structsMapping {
								mappingDir, _ := filepath.Split(mappingPath)

								if strings.HasSuffix(mappingDir, "/") {
									mappingDir = mappingDir[:len(mappingDir)-1]
								}
								if !strings.HasSuffix(nodeImportPath, mappingDir) {
									continue
								}

								if typeSpec, ok := structs[t.Sel.Name]; ok && typeSpec != nil {
									if err := parseStruct(typeSpec, requestBodyStruct); err != nil {
										log.Println(err)
										continue
									}
									parsed = true
									break root
								}
							}
						}
					}
				}
			}
		}
	}

	if !parsed {
		return false
	}

	requestBodyStructInstance := requestBodyStruct.Build().New()

	schemaRef, _, err := openapi3gen.NewSchemaRefForValue(requestBodyStructInstance)
	if err != nil {
		panic(err)
	}

	requestBody := openapi3.NewRequestBody().
		WithJSONSchemaRef(schemaRef)

	// TODO: someRequestBody
	// request body ref
	openapi.Components.RequestBodies[operationName] = &openapi3.RequestBodyRef{
		Value: requestBody,
	}

	operation := openAPIOperationByMethod(openapi.Paths[route.Path], route.Method)

	if operation == nil {
		log.Println("operation not found, [path=%s, method=%s]", route.Path, route.Method)
		return false
	}

	operation.RequestBody.Value = requestBody

	// parse body argument

	return true
}

func parseStruct(typeSpec *ast.TypeSpec, structBuilder dynamicstruct.Builder) error {
	structSpec, ok := typeSpec.Type.(*ast.StructType)

	if !ok {
		return errors.New("invalid type")
	}

	for _, field := range structSpec.Fields.List {
		if field.Tag == nil {
			continue
		}
		tags, err := structtag.Parse(strings.ReplaceAll(field.Tag.Value, "`", ""))
		if err != nil {
			return err
		}

		tag, err := tags.Get("json")
		if err != nil {
			return err
		}

		if f, ok := field.Type.(*ast.Ident); ok {
			name := field.Names[0].Name // TODO: [0] is ok? why its an array
			structBuilder.AddField(name, getType(f.Name), fmt.Sprintf(`json:"%s"`, tag.Name))
		} else if f, ok := field.Type.(*ast.SelectorExpr); ok {
			x := f.X.(*ast.Ident)

			switch t := getType(x.Name).(type) {
			case *time.Time:
				name := field.Names[0].Name
				structBuilder.AddField(name, t, fmt.Sprintf(`json:"%s"`, tag.Name))
			}
		}
	}

	return nil
}

func matchCallExpression(operationName string, searchIdentifiers []*SearchIdentifier, exp ast.Expr) (callExpr *ast.CallExpr, foundArgumentPosition int, found bool) {
	var selectorExpr *ast.SelectorExpr

	if operationName == debugOperationMethod {
		fmt.Sprintf("d")
	}

	call, ok := exp.(*ast.CallExpr)

	if !ok {
		return
	}

	selectorExpr, ok = call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}

	//selectorExpr = s
	//
	//if callOk {
	//
	//	return
	//} else {
	//	composeList, ok := exp.(*ast.CompositeLit)
	//	if !ok {
	//		return
	//	}
	//
	//	s, ok := composeList.Type.(*ast.SelectorExpr)
	//	if !ok {
	//		return
	//	}
	//
	//	selectorExpr = s
	//}

	if operationName == debugOperationMethod {
		fmt.Sprintf("d")
	}

	for _, identifier := range searchIdentifiers {
		ids := make([]string, len(identifier.MethodStatement))
		copy(ids, identifier.MethodStatement)

		reverseSliceString(ids)

		expect := len(ids)
		current := 0

		for i, id := range ids {
			nextId := ""

			if len(ids)-1 >= i+1 {
				nextId = ids[i+1]
			}

			if selectorExpr.Sel.Name != id {
				break
			}

			current++

			switch t := selectorExpr.X.(type) {
			case *ast.CallExpr:
				if s, ok := t.Fun.(*ast.SelectorExpr); ok {
					selectorExpr = s
				}
			case *ast.SelectorExpr:
				selectorExpr = t
			case *ast.Ident:
				if t.Obj == nil {
					selectorExpr = &ast.SelectorExpr{ // hack for method expression e.g  ["iris", "Context", "ReadJSON"] => ctx.ReadJSON
						Sel: &ast.Ident{
							Name: t.Name,
						},
					}

					continue
				}

				field, ok := t.Obj.Decl.(*ast.Field)
				if !ok {
					continue
				}

				s, ok := field.Type.(*ast.SelectorExpr)
				if ok {
					selectorExpr = s
					continue
				}

				starExpr, ok := field.Type.(*ast.StarExpr)
				if !ok {
					continue
				}

				ident, ok := starExpr.X.(*ast.Ident)
				if !ok {
					continue
				}

				typeSpec, ok := ident.Obj.Decl.(*ast.TypeSpec)
				if !ok {
					continue
				}

				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}

				for _, field := range structType.Fields.List {
					switch t := field.Type.(type) {
					case *ast.StarExpr: // if pointer e.g *baseController
						ptr, ok := field.Type.(*ast.StarExpr)
						if !ok {
							continue
						}

						ident, ok := ptr.X.(*ast.Ident)
						if !ok {
							continue
						}

						if operationName == debugOperationMethod {
							fmt.Sprintf("d")
						}

						if ident.Name != nextId {
							continue
						}

						selectorExpr = &ast.SelectorExpr{ // hack for method shortcut e.g api.BaseController.Abc() and api.Abc()
							Sel: &ast.Ident{
								Name: ident.Name,
							},
						}

						break
					case *ast.Ident: // if interface e.g BaseController
						if operationName == debugOperationMethod {
							fmt.Sprintf("d")
						}

						if t.Name != nextId {
							continue
						}

						if operationName == debugOperationMethod {
							fmt.Sprintf("d")
						}

						selectorExpr = &ast.SelectorExpr{ // hack for method shortcut e.g api.BaseController.Abc() and api.Abc()
							Sel: &ast.Ident{
								Name: t.Name,
							},
						}

						break
					}

				}
			}
		}

		if expect == current {
			return call, identifier.ArgumentPosition, true
		}
	}

	return
}

func mapStructsFromFiles(root string) (map[string]map[string]*ast.TypeSpec, error) {
	walkRook := "."
	if root != "" {
		walkRook = root
	}

	ignored, err := gogitignore.CompileIgnoreFile("./gitignore")
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// per file path, per struct name
	out := make(map[string]map[string]*ast.TypeSpec)

	// TODO: pass root folder to omit every folder checking
	return out, filepath.Walk(walkRook, func(path string, f os.FileInfo, err error) error {
		if ignored != nil {
			if ignored.MatchesPath(path) {
				return filepath.SkipDir
			}
		}

		// TODO: respect .gitignore
		switch path {
		case ".git":
			return filepath.SkipDir
		}

		// TODO: omit <name>_test.go
		if filepath.Ext(path) != ".go" {
			return nil
		}

		if root != "" {
			if is, _ := isSubPath(root, path); !is {
				return nil
			}
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, parser.ParseComments) // 1. parse AST for router source file
		if err != nil {
			return err
		}

		// TODO: check if absolute - if yes remove prefix  ~/Code/zdunecki -> prefix

		if _, ok := out[path]; !ok {
			out[path] = make(map[string]*ast.TypeSpec)
		}

		if node.Scope != nil {
			for _, v := range node.Scope.Objects {
				typeSpec, ok := v.Decl.(*ast.TypeSpec)
				if !ok {
					continue
				}

				if _, ok := out[path][typeSpec.Name.Name]; !ok {
					out[path][typeSpec.Name.Name] = typeSpec
				}
			}
		}

		return nil
	})
}
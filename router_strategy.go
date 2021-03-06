package restflix

import (
	"fmt"
	"reflect"
	"regexp"
	"runtime"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

func getFunctionName(i interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
}

// TODO: reject middlewares - only last method from router
// TODO: support multiple method declarations in different files
func irisRouterStrategy(options *Options, openapi *openapi3.T) error {
	app, searchIdentifiers, structsMappingRootPath, goModName, ignoreRoutes, require :=
		options.iris, options.SearchIdentifiers, options.StructsMappingRootPath, options.GoModName, options.IgnoreRoutes, options.Require

	structsMapping, err := mapStructsFromFiles(options.GoModName, structsMappingRootPath, require, false)
	if err != nil {
		return err
	}

root:
	for _, route := range app.APIBuilder.GetRoutes() {
		if ignoreRoutes != nil {
			for _, pattern := range ignoreRoutes {
				found, _ := regexp.MatchString(pattern, route.Path)
				if found {
					continue root
				}
			}
		}

		sourceFileName := route.SourceFileName
		findMethod := route.MainHandlerName // github.com/livesession/restflix/test/app.(*api).testBaseController-fm

		lastHandler := route.Handlers[len(route.Handlers)-1]
		handlerName := getFunctionName(lastHandler)

		p := strings.Split(handlerName, "/")
		findMethod = p[len(p)-1]
		findMethod = strings.TrimSuffix(findMethod, compilerClousureSuffix) // app.(*api).testBaseController

		fullReferenceSplitter := strings.Split(findMethod, ".")
		findMethod = fullReferenceSplitter[len(fullReferenceSplitter)-1] // testBaseController

		operationName := fmt.Sprintf("[%s]%s", route.Method, strings.ReplaceAll(route.Path, "/", "-"))
		if operationName == debugOperationMethod {
			fmt.Sprintf("d")
		}

		parseRouterMethod(openapi, sourceFileName, findMethod, route, structsMapping, searchIdentifiers, goModName)
	}

	return nil
}

// Copyright 2020-2024, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package python

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/pulumi/pulumi/pkg/v3/codegen"
	"github.com/pulumi/pulumi/pkg/v3/codegen/hcl2/model"
	"github.com/pulumi/pulumi/pkg/v3/codegen/hcl2/syntax"
	"github.com/pulumi/pulumi/pkg/v3/codegen/pcl"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
	"github.com/zclconf/go-cty/cty"
)

func (g *generator) rewriteTraversal(traversal hcl.Traversal, source model.Expression,
	parts []model.Traversable,
) model.Expression {
	// TODO(pdg): transfer trivia

	var rootName string
	var currentTraversal hcl.Traversal
	currentParts := []model.Traversable{parts[0]}
	currentExpression := source

	if len(traversal) > 0 {
		if root, isRoot := traversal[0].(hcl.TraverseRoot); isRoot {
			traversal = traversal[1:]
			rootName, currentTraversal = root.Name, hcl.Traversal{root}
		}
	}

	for i, traverser := range traversal {
		var key cty.Value
		switch traverser := traverser.(type) {
		case hcl.TraverseAttr:
			key = cty.StringVal(traverser.Name)
		case hcl.TraverseIndex:
			key = traverser.Key
		default:
			contract.Failf("unexpected traverser of type %T (%v)", traverser, traverser.SourceRange())
		}

		if key.Type() != cty.String {
			currentTraversal = append(currentTraversal, traverser)
			currentParts = append(currentParts, parts[i+1])
			continue
		}

		keyVal, objectKey := key.AsString(), false

		receiver := parts[i]
		_, hasSchema := pcl.GetSchemaForType(model.GetTraversableType(receiver))
		_, isComponent := receiver.(*pcl.Component)

		if hasSchema || isComponent {
			objectKey = true
			switch t := traverser.(type) {
			case hcl.TraverseAttr:
				t.Name = keyVal
				traverser, traversal[i] = t, t
			case hcl.TraverseIndex:
				t.Key = cty.StringVal(keyVal)
				traverser, traversal[i] = t, t
			}
		}

		if objectKey && isLegalIdentifier(keyVal) {
			currentTraversal = append(currentTraversal, traverser)
			if i < len(traversal)-1 {
				currentParts = append(currentParts, parts[i+1])
			}
			continue
		}

		if currentExpression == nil {
			currentExpression = &model.ScopeTraversalExpression{
				RootName:  rootName,
				Traversal: currentTraversal,
				Parts:     currentParts,
			}

			currentTraversal, currentParts = nil, nil
		} else if len(currentTraversal) > 0 {
			currentExpression = &model.RelativeTraversalExpression{
				Source:    currentExpression,
				Traversal: currentTraversal,
				Parts:     currentParts,
			}
			currentTraversal, currentParts = nil, []model.Traversable{currentExpression.Type()}
		}

		currentExpression = &model.IndexExpression{
			Collection: currentExpression,
			Key: &model.LiteralValueExpression{
				Value: cty.StringVal(keyVal),
			},
		}

		// typechecking the index expression will compute the type of the expression
		// so that when we call Type() later on, we get the correct type.
		typecheckDiags := currentExpression.Typecheck(true)
		g.diagnostics = g.diagnostics.Extend(typecheckDiags)
	}

	if currentExpression == nil {
		currentExpression = &model.ScopeTraversalExpression{
			RootName:  rootName,
			Traversal: currentTraversal,
			Parts:     currentParts,
		}
	} else if len(currentTraversal) > 0 {
		currentExpression = &model.RelativeTraversalExpression{
			Source:    currentExpression,
			Traversal: currentTraversal,
			Parts:     currentParts,
		}
	}

	if currentExpression == source {
		return nil
	}

	return currentExpression
}

type quoteTemp struct {
	Name         string
	VariableType model.Type
	Value        model.Expression
}

func (qt *quoteTemp) Type() model.Type {
	return qt.VariableType
}

func (qt *quoteTemp) Traverse(traverser hcl.Traverser) (model.Traversable, hcl.Diagnostics) {
	return qt.VariableType.Traverse(traverser)
}

func (qt *quoteTemp) SyntaxNode() hclsyntax.Node {
	return syntax.None
}

type quoteAllocations struct {
	quotes map[model.Expression]string
	temps  []*quoteTemp
}

type quoteAllocator struct {
	allocations *quoteAllocations
	allocated   codegen.StringSet
	stack       []model.Expression
}

func (qa *quoteAllocator) allocate(longString bool) (string, bool) {
	if longString {
		if !qa.allocated.Has(`"`) && !qa.allocated.Has(`"""`) {
			qa.allocated.Add(`"""`)
			return `"""`, true
		}

		if !qa.allocated.Has(`'`) && !qa.allocated.Has(`'''`) {
			qa.allocated.Add(`'''`)
			return `'''`, true
		}

		return "", false
	}

	if !qa.allocated.Has(`"`) {
		qa.allocated.Add(`"`)
		return `"`, true
	}

	if !qa.allocated.Has(`'`) {
		qa.allocated.Add(`'`)
		return `'`, true
	}

	return "", false
}

func (qa *quoteAllocator) free(quotes string) {
	qa.allocated.Delete(quotes)
}

func (qa *quoteAllocator) inTemplate() bool {
	if len(qa.stack) < 2 {
		return false
	}
	_, isTemplate := qa.stack[len(qa.stack)-2].(*model.TemplateExpression)
	return isTemplate
}

func (qa *quoteAllocator) allocateExpression(x model.Expression) (model.Expression, hcl.Diagnostics) {
	qa.stack = append(qa.stack, x)

	var longString bool
	switch x := x.(type) {
	case *model.LiteralValueExpression:
		if !model.StringType.AssignableFrom(x.Type()) || qa.inTemplate() {
			return x, nil
		}
		v := x.Value.AsString()
		switch strings.Count(v, "\n") {
		case 0:
			// OK
		case 1:
			longString = v[0] != '\n' && v[len(v)-1] != '\n'
		default:
			longString = true
		}
	case *model.TemplateExpression:
		for i, part := range x.Parts {
			if lit, ok := part.(*model.LiteralValueExpression); ok && model.StringType.AssignableFrom(lit.Type()) {
				v := lit.Value.AsString()
				switch strings.Count(v, "\n") {
				case 0:
					continue
				case 1:
					if i == 0 && v[0] == '\n' || i == len(x.Parts)-1 && v[len(v)-1] == '\n' {
						continue
					}
				}
				longString = true
				break
			}
		}
	default:
		return x, nil
	}

	if quote, ok := qa.allocate(longString); ok {
		qa.allocations.quotes[x] = quote
		return x, nil
	}

	allocator := &quoteAllocator{allocated: codegen.StringSet{}, allocations: qa.allocations}
	value, valueDiags := model.VisitExpression(x, allocator.allocateExpression, allocator.freeExpression)

	temp := &quoteTemp{
		Name:         fmt.Sprintf("str%d", len(qa.allocations.temps)),
		VariableType: x.Type(),
		Value:        value,
	}
	qa.allocations.temps = append(qa.allocations.temps, temp)

	return &model.ScopeTraversalExpression{
		RootName:  temp.Name,
		Traversal: hcl.Traversal{hcl.TraverseRoot{Name: ""}},
		Parts:     []model.Traversable{temp},
	}, valueDiags
}

func (qa *quoteAllocator) freeExpression(x model.Expression) (model.Expression, hcl.Diagnostics) {
	defer func() {
		qa.stack = qa.stack[:len(qa.stack)-1]
	}()

	switch x := x.(type) {
	case *model.LiteralValueExpression:
		if !model.StringType.AssignableFrom(x.Type()) || qa.inTemplate() {
			return x, nil
		}
		// OK
	case *model.TemplateExpression:
		// OK
	default:
		return x, nil
	}

	quotes, ok := qa.allocations.quotes[x]
	contract.Assertf(ok, "cannot free unknown expression")
	qa.free(quotes)
	return x, nil
}

func (g *generator) rewriteQuotes(x model.Expression) (model.Expression, []*quoteTemp, hcl.Diagnostics) {
	var diagnostics hcl.Diagnostics

	// First, rewrite traversals that require string indices into index expressions.
	x, rewriteDiags := model.VisitExpression(x, nil, func(x model.Expression) (model.Expression, hcl.Diagnostics) {
		switch x := x.(type) {
		case *model.RelativeTraversalExpression:
			idx := g.rewriteTraversal(x.Traversal, x.Source, x.Parts)
			if idx != nil {
				return idx, nil
			}
		case *model.ScopeTraversalExpression:
			idx := g.rewriteTraversal(x.Traversal, nil, x.Parts)
			if idx != nil {
				return idx, nil
			}
		}
		return x, nil
	})
	diagnostics = append(diagnostics, rewriteDiags...)

	// Then lift any expressions that cannot be allocated quotes into temps.
	allocations := &quoteAllocations{
		quotes: g.quotes,
	}
	allocator := &quoteAllocator{allocated: codegen.StringSet{}, allocations: allocations}
	x, rewriteDiags = model.VisitExpression(x, allocator.allocateExpression, allocator.freeExpression)
	diagnostics = append(diagnostics, rewriteDiags...)

	return x, allocations.temps, diagnostics
}

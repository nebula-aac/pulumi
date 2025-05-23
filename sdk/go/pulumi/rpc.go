// Copyright 2016-2018, Pulumi Corporation.
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

package pulumi

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/blang/semver"
	"golang.org/x/exp/maps"
	"golang.org/x/net/context"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	rarchive "github.com/pulumi/pulumi/sdk/v3/go/common/resource/archive"
	rasset "github.com/pulumi/pulumi/sdk/v3/go/common/resource/asset"
	"github.com/pulumi/pulumi/sdk/v3/go/common/slice"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/contract"
	"github.com/pulumi/pulumi/sdk/v3/go/internal"
)

// addDependency adds a dependency on the given resource to the set of deps.
//
// The behavior of this method depends on whether or not the resource is a custom resource, a local component resource,
// a remote component resource, a dependency resource, or a rehydrated component resource:
//
//   - Custom resources are added directly to the set, as they are "real" nodes in the dependency graph.
//   - Local component resources act as aggregations of their descendents. Rather than adding the component resource
//     itself, each child resource is added as a dependency.
//   - Remote component resources are added directly to the set, as they naturally act as aggregations of their children
//     with respect to dependencies: the construction of a remote component always waits on the construction of its
//     children.
//   - Dependency resources are added directly to the set.
//   - Rehydrated component resources are added directly to the set.
//
// In other words, if we had:
//
//			  Comp1
//		  /     |     \
//	  Cust1   Comp2  Remote1
//			  /   \       \
//		  Cust2   Cust3  Comp3
//		  /                 \
//	  Cust4                Cust5
//
// Then the transitively reachable resources of Comp1 will be [Cust1, Cust2, Cust3, Remote1].
// It will *not* include:
// * Cust4 because it is a child of a custom resource
// * Comp2 because it is a non-remote component resoruce
// * Comp3 and Cust5 because Comp3 is a child of a remote component resource
func addDependency(ctx context.Context, deps map[URN]Resource, res, from Resource) error {
	if _, custom := res.(CustomResource); !custom {
		// If `res` is the same as `from`, exit early to avoid depending on
		// children that haven't been registered yet.
		if res == from {
			return nil
		}

		for _, child := range res.getChildren() {
			if err := addDependency(ctx, deps, child, from); err != nil {
				return err
			}
		}
		// keepDependency() returns true for remote component resources, dependency resources,
		// and rehydrated component resources.
		if !res.keepDependency() {
			return nil
		}
	}

	urn, _, _, err := res.URN().awaitURN(ctx)
	if err != nil {
		return err
	}
	deps[urn] = res
	return nil
}

// expandDependencies expands the given slice of Resources into a set of URNs.
func expandDependencies(ctx context.Context, deps []Resource) (map[URN]Resource, error) {
	set := map[URN]Resource{}
	for _, r := range deps {
		if err := addDependency(ctx, set, r, nil /* from */); err != nil {
			return nil, err
		}
	}
	return set, nil
}

// marshalOptions controls the options for marshaling inputs.
type marshalOptions struct {
	// Set to true to error if any Outputs are present; otherwise Outputs will be awaited.
	ErrorOnOutput bool

	// Set to true to exclude resource references from the set of dependencies identified
	// during marshaling. This is useful for remote components (i.e. MLCs) where we want
	// propertyDependencies to be empty for a property that only contains resource
	// references.
	ExcludeResourceRefsFromDeps bool
}

// marshalInputs turns resource property inputs into a map suitable for marshaling.
func marshalInputs(props Input) (resource.PropertyMap, map[string][]URN, []URN, error) {
	return marshalInputsOptions(props, nil)
}

// marshalInputs turns resource property inputs into a map suitable for marshaling.
func marshalInputsOptions(props Input, opts *marshalOptions) (resource.PropertyMap, map[string][]URN, []URN, error) {
	deps := map[URN]struct{}{}
	pmap, pdeps := resource.PropertyMap{}, map[string][]URN{}

	if props == nil {
		return pmap, pdeps, nil, nil
	}

	marshalProperty := func(pname string, pv interface{}, pt reflect.Type) error {
		// Get the underlying value, possibly waiting for an output to arrive.
		v, resourceDeps, err := marshalInputOptions(pv, pt, opts)
		if err != nil {
			return fmt.Errorf("awaiting input property %q: %w", pname, err)
		}

		// Record all dependencies accumulated from reading this property.
		allDeps, err := expandDependencies(context.TODO(), resourceDeps)
		if err != nil {
			return err
		}
		for k := range allDeps {
			deps[k] = struct{}{}
		}

		if !v.IsNull() || len(allDeps) > 0 {
			urns := slice.Prealloc[URN](len(allDeps))
			for v := range allDeps {
				urns = append(urns, v)
			}
			pmap[resource.PropertyKey(pname)] = v
			pdeps[pname] = urns
		}
		return nil
	}

	pv := reflect.ValueOf(props)
	if pv.Kind() == reflect.Ptr {
		if pv.IsNil() {
			return pmap, pdeps, nil, nil
		}
		pv = pv.Elem()
	}
	pt := pv.Type()

	rt := props.ElementType()
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}

	//nolint:exhaustive // We only need to handle the types we care about.
	switch pt.Kind() {
	case reflect.Struct:
		contract.Assertf(rt.Kind() == reflect.Struct, "expected struct, got %v (%v)", rt, rt.Kind())
		// We use the resolved type to decide how to convert inputs to outputs.
		rt := props.ElementType()
		if rt.Kind() == reflect.Ptr {
			rt = rt.Elem()
		}
		getMappedField := internal.MapStructTypes(pt, rt)
		// Now, marshal each field in the input.
		numFields := pt.NumField()
		for i := 0; i < numFields; i++ {
			destField, _ := getMappedField(reflect.Value{}, i)
			tag := destField.Tag.Get("pulumi")
			tag = strings.Split(tag, ",")[0] // tagName,flag => tagName
			if tag == "" {
				continue
			}
			err := marshalProperty(tag, pv.Field(i).Interface(), destField.Type)
			if err != nil {
				return nil, nil, nil, err
			}
		}
	case reflect.Map:
		ktype := rt.Key()
		contract.Assertf(ktype.Kind() == reflect.String,
			"expected map with string keys, got %v (%v)", ktype, ktype.Kind())
		for _, key := range pv.MapKeys() {
			keyname := key.Interface().(string)
			val := pv.MapIndex(key).Interface()
			err := marshalProperty(keyname, val, rt.Elem())
			if err != nil {
				return nil, nil, nil, err
			}
		}
	default:
		return nil, nil, nil, fmt.Errorf("cannot marshal Input that is not a struct or map, saw type %s", pt.String())
	}

	urns := slice.Prealloc[URN](len(deps))
	for v := range deps {
		urns = append(urns, v)
	}
	return pmap, pdeps, urns, nil
}

// `gosec` thinks these are credentials, but they are not.
//
//nolint:gosec
const rpcTokenUnknownValue = "04da6b54-80e4-46f7-96ec-b56ff0331ba9"

const cannotAwaitFmt = "cannot marshal Output value of type %T; please use Apply to access the Output's value"

// marshalInput marshals an input value, returning its raw serializable value along with any dependencies.
func marshalInput(v interface{}, destType reflect.Type) (resource.PropertyValue, []Resource, error) {
	return marshalInputOptions(v, destType, nil)
}

// marshalInput marshals an input value, returning its raw serializable value along with any dependencies.
func marshalInputOptions(
	v interface{}, destType reflect.Type, opts *marshalOptions,
) (resource.PropertyValue, []Resource, error) {
	return marshalInputOptionsImpl(v, destType, opts, false /*skipInputCheck*/)
}

// marshalInputImpl marshals an input value, returning its raw serializable value along with any dependencies.
func marshalInputOptionsImpl(v interface{},
	destType reflect.Type,
	opts *marshalOptions,
	skipInputCheck bool,
) (resource.PropertyValue, []Resource, error) {
	var deps []Resource
	for {
		valueType := reflect.TypeOf(v)

		// If this is an Input, make sure it is of the proper type and await it if it is an output/
		if input, ok := v.(Input); !skipInputCheck && ok {
			if inputType := reflect.ValueOf(input); inputType.Kind() == reflect.Ptr && inputType.IsNil() {
				// input type is a ptr type with a nil backing value
				return resource.PropertyValue{}, nil, nil
			}
			valueType = input.ElementType()

			// Handle cases where the destination is a ptr type whose element type is the same as the value type
			// (e.g. destType is *FooBar and valueType is FooBar).
			// This avoids calling the ToOutput method to convert the input to an output in this case.
			if valueType != destType && destType.Kind() == reflect.Ptr && valueType == destType.Elem() {
				destType = destType.Elem()
			}

			// If the element type of the input is not identical to the type of the destination and the destination is
			// not the any type (i.e. interface{}), attempt to convert the input to an appropriately-typed output.
			if valueType != destType && destType != anyType {
				if newOutput, ok := internal.CallToOutputMethod(context.TODO(), reflect.ValueOf(input), destType); ok {
					// We were able to convert the input. Use the result as the new input value.
					input, valueType = newOutput, destType
				} else if !valueType.AssignableTo(destType) {
					err := fmt.Errorf(
						"cannot marshal an input of type %T with element type %v as a value of type %v",
						input, valueType, destType)
					return resource.PropertyValue{}, nil, err
				}
			}

			// If the input is an Output, await its value. The returned value is fully resolved.
			if output, ok := input.(Output); ok {
				if opts != nil && opts.ErrorOnOutput {
					return resource.PropertyValue{}, nil, fmt.Errorf(cannotAwaitFmt, output)
				}

				// Await the output.
				ov, known, secret, outputDeps, err := awaitWithContext(context.TODO(), output)
				if err != nil {
					return resource.PropertyValue{}, nil, err
				}

				// Get the underlying value, if known.
				var element resource.PropertyValue
				if known {
					element, _, err = marshalInputOptionsImpl(ov, destType, opts, true /*skipInputCheck*/)
					if err != nil {
						return resource.PropertyValue{}, nil, err
					}

					// If it's known, not a secret, and has no deps, return the value itself.
					if !secret && len(outputDeps) == 0 {
						return element, nil, nil
					}
				}

				// Expand dependencies.
				depSet, err := expandDependencies(context.TODO(), outputDeps)
				if err != nil {
					return resource.PropertyValue{}, nil, err
				}
				var dependencies []resource.URN
				if len(depSet) > 0 {
					dependencies = make([]resource.URN, len(depSet))
					urns := maps.Keys(depSet)
					sort.Slice(urns, func(i, j int) bool { return urns[i] < urns[j] })
					for i, urn := range urns {
						dependencies[i] = resource.URN(urn)
					}
				}

				return resource.NewOutputProperty(resource.Output{
					Element:      element,
					Known:        known,
					Secret:       secret,
					Dependencies: dependencies,
				}), outputDeps, nil
			}
		}

		// Set skipInputCheck to false, so that if we loop around we don't skip the input check.
		skipInputCheck = false

		// If v is nil, just return that.
		if v == nil {
			return resource.PropertyValue{}, nil, nil
		} else if val := reflect.ValueOf(v); val.Kind() == reflect.Ptr && val.IsNil() {
			// Here we round trip through a reflect.Value to catch fat pointers of the
			// form
			//
			// 	<SomeType><nil value>
			//
			// This prevents calling methods on nil pointers when we cast to an interface
			// (like `Resource`)
			return resource.PropertyValue{}, nil, nil
		}

		// Look for some well known types.
		switch v := v.(type) {
		case *asset:
			if v.invalid {
				return resource.PropertyValue{}, nil, errors.New("invalid asset")
			}
			return resource.NewAssetProperty(&rasset.Asset{
				Path: v.Path(),
				Text: v.Text(),
				URI:  v.URI(),
			}), deps, nil
		case *archive:
			if v.invalid {
				return resource.PropertyValue{}, nil, errors.New("invalid archive")
			}

			var assets map[string]interface{}
			if as := v.Assets(); as != nil {
				assets = make(map[string]interface{})
				for k, a := range as {
					aa, _, err := marshalInputOptions(a, anyType, opts)
					if err != nil {
						return resource.PropertyValue{}, nil, err
					}
					assets[k] = aa.V
				}
			}
			return resource.NewArchiveProperty(&rarchive.Archive{
				Assets: assets,
				Path:   v.Path(),
				URI:    v.URI(),
			}), deps, nil
		case Resource:
			if opts == nil || !opts.ExcludeResourceRefsFromDeps {
				deps = append(deps, v)
			}

			urn, known, secretURN, err := v.URN().awaitURN(context.Background())
			if err != nil {
				return resource.PropertyValue{}, nil, err
			}
			contract.Assertf(known, "URN must be known")
			contract.Assertf(!secretURN, "URN must not be secret")

			if custom, ok := v.(CustomResource); ok {
				id, _, secretID, err := custom.ID().awaitID(context.Background())
				if err != nil {
					return resource.PropertyValue{}, nil, err
				}
				contract.Assertf(!secretID, "CustomResource must not have a secret ID")

				return resource.MakeCustomResourceReference(resource.URN(urn), resource.ID(id), ""), deps, nil
			}

			return resource.MakeComponentResourceReference(resource.URN(urn), ""), deps, nil
		}

		if destType.Kind() == reflect.Interface {
			// This happens in the case of Any.
			if valueType.Kind() == reflect.Interface {
				valueType = reflect.TypeOf(v)
			}
			destType = valueType
		}

		rv := reflect.ValueOf(v)

		if rv.Type().Kind() == reflect.Array || rv.Type().Kind() == reflect.Slice || rv.Type().Kind() == reflect.Map {
			// Not assignable in prompt form because of the difference in input and output shapes.
			//
			// TODO(7434): update these checks once fixed.
		} else {
			contract.Assertf(valueType.AssignableTo(destType) || valueType.ConvertibleTo(destType),
				"%v: cannot assign %v to %v", v, valueType, destType)
		}

		//nolint:exhaustive // We only need to handle the types we care about.
		switch rv.Type().Kind() {
		case reflect.Bool:
			return resource.NewBoolProperty(rv.Bool()), deps, nil
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return resource.NewNumberProperty(float64(rv.Int())), deps, nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return resource.NewNumberProperty(float64(rv.Uint())), deps, nil
		case reflect.Float32, reflect.Float64:
			return resource.NewNumberProperty(rv.Float()), deps, nil
		case reflect.Ptr, reflect.Interface:
			// Dereference non-nil pointers and interfaces.
			if rv.IsNil() {
				return resource.PropertyValue{}, deps, nil
			}
			if destType.Kind() == reflect.Ptr {
				destType = destType.Elem()
			}
			v = rv.Elem().Interface()
			continue
		case reflect.String:
			return resource.NewStringProperty(rv.String()), deps, nil
		case reflect.Array, reflect.Slice:
			if rv.IsNil() {
				return resource.PropertyValue{}, deps, nil
			}

			destElem := destType.Elem()

			// If an array or a slice, create a new array by recursing into elements.
			arr := make([]resource.PropertyValue, 0, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				elem := rv.Index(i)
				e, d, err := marshalInputOptions(elem.Interface(), destElem, opts)
				if err != nil {
					return resource.PropertyValue{}, nil, err
				}
				arr = append(arr, e)
				deps = append(deps, d...)
			}
			return resource.NewArrayProperty(arr), deps, nil
		case reflect.Map:
			if rv.Type().Key().Kind() != reflect.String {
				return resource.PropertyValue{}, nil,
					fmt.Errorf("expected map keys to be strings; got %v", rv.Type().Key())
			}

			if rv.IsNil() {
				return resource.PropertyValue{}, deps, nil
			}

			destElem := destType.Elem()

			// For maps, only support string-based keys, and recurse into the values.
			obj := resource.PropertyMap{}
			for _, key := range rv.MapKeys() {
				value := rv.MapIndex(key)
				mv, d, err := marshalInputOptions(value.Interface(), destElem, opts)
				if err != nil {
					return resource.PropertyValue{}, nil, err
				}
				if !mv.IsNull() {
					obj[resource.PropertyKey(key.String())] = mv
				}
				deps = append(deps, d...)
			}
			return resource.NewObjectProperty(obj), deps, nil
		case reflect.Struct:
			obj := resource.PropertyMap{}
			typ := rv.Type()
			getMappedField := internal.MapStructTypes(typ, destType)
			for i := 0; i < typ.NumField(); i++ {
				destField, _ := getMappedField(reflect.Value{}, i)
				tag := destField.Tag.Get("pulumi")
				tag = strings.Split(tag, ",")[0] // tagName,flag => tagName
				if tag == "" {
					continue
				}

				fv, d, err := marshalInputOptions(rv.Field(i).Interface(), destField.Type, opts)
				if err != nil {
					return resource.PropertyValue{}, nil, err
				}

				if !fv.IsNull() {
					obj[resource.PropertyKey(tag)] = fv
				}
				deps = append(deps, d...)
			}
			return resource.NewObjectProperty(obj), deps, nil
		}
		return resource.PropertyValue{}, nil, fmt.Errorf("unrecognized input property type: %v (%T)", v, v)
	}
}

func unmarshalResourceReference(ctx *Context, ref resource.ResourceReference) (Resource, error) {
	version := nullVersion
	if len(ref.PackageVersion) > 0 {
		var err error
		version, err = semver.ParseTolerant(ref.PackageVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to parse provider version: %s", ref.PackageVersion)
		}
	}

	resName := ref.URN.Name()
	resType := ref.URN.Type()

	isProvider := tokens.Token(resType).HasModuleMember() && resType.Module() == "pulumi:providers"
	if isProvider {
		pkgName := resType.Name().String()
		if resourcePackageV, ok := resourcePackages.Load(pkgName, version); ok {
			resourcePackage := resourcePackageV.(ResourcePackage)
			return resourcePackage.ConstructProvider(ctx, resName, string(resType), string(ref.URN))
		}
		id, _ := ref.IDString()
		return ctx.newDependencyProviderResource(URN(ref.URN), ID(id)), nil
	}

	modName := resType.Module().String()
	if resourceModuleV, ok := resourceModules.Load(modName, version); ok {
		resourceModule := resourceModuleV.(ResourceModule)
		return resourceModule.Construct(ctx, resName, string(resType), string(ref.URN))
	}
	if id, hasID := ref.IDString(); hasID {
		return ctx.newDependencyCustomResource(URN(ref.URN), ID(id)), nil
	}
	return ctx.newDependencyResource(URN(ref.URN)), nil
}

func unmarshalPropertyValue(ctx *Context, v resource.PropertyValue) (interface{}, bool, error) {
	switch {
	case v.IsComputed():
		return nil, false, nil
	case v.IsOutput():
		if !v.OutputValue().Known {
			return nil, v.OutputValue().Secret, nil
		}
		ov, _, err := unmarshalPropertyValue(ctx, v.OutputValue().Element)
		if err != nil {
			return nil, false, err
		}
		return ov, v.OutputValue().Secret, nil
	case v.IsSecret():
		sv, _, err := unmarshalPropertyValue(ctx, v.SecretValue().Element)
		if err != nil {
			return nil, false, err
		}
		return sv, true, nil
	case v.IsArray():
		arr := v.ArrayValue()
		rv := make([]interface{}, len(arr))
		secret := false
		for i, e := range arr {
			ev, esecret, err := unmarshalPropertyValue(ctx, e)
			secret = secret || esecret
			if err != nil {
				return nil, false, err
			}
			rv[i] = ev
		}
		return rv, secret, nil
	case v.IsObject():
		m := make(map[string]interface{})
		secret := false
		for k, e := range v.ObjectValue() {
			ev, esecret, err := unmarshalPropertyValue(ctx, e)
			secret = secret || esecret
			if err != nil {
				return nil, false, err
			}
			m[string(k)] = ev
		}
		return m, secret, nil
	case v.IsAsset():
		asset := v.AssetValue()
		switch {
		case asset.IsPath():
			return NewFileAsset(asset.Path), false, nil
		case asset.IsText():
			return NewStringAsset(asset.Text), false, nil
		case asset.IsURI():
			return NewRemoteAsset(asset.URI), false, nil
		}
		return nil, false, errors.New("expected asset to be one of File, String, or Remote; got none")
	case v.IsArchive():
		archive := v.ArchiveValue()
		secret := false
		switch {
		case archive.IsAssets():
			as := make(map[string]interface{})
			for k, v := range archive.Assets {
				a, asecret, err := unmarshalPropertyValue(ctx, resource.NewPropertyValue(v))
				secret = secret || asecret
				if err != nil {
					return nil, false, err
				}
				as[k] = a
			}
			return NewAssetArchive(as), secret, nil
		case archive.IsPath():
			return NewFileArchive(archive.Path), secret, nil
		case archive.IsURI():
			return NewRemoteArchive(archive.URI), secret, nil
		}
		return nil, false, errors.New("expected asset to be one of File, String, or Remote; got none")
	case v.IsResourceReference():
		resource, err := unmarshalResourceReference(ctx, v.ResourceReferenceValue())
		if err != nil {
			return nil, false, err
		}
		return resource, false, nil
	default:
		return v.V, false, nil
	}
}

// unmarshalPropertyMap is used to turn the values in a resource.PropertyMap into sensible runtime types. This tries to
// keep things as plain types where possible (e.g. a string property value will just be a `pulumi.String`, not an
// `OutputString`). It will use `pulumi.Output` for values that are either Computed (will always be a
// `pulumi.AnyOutput`), secret, or an output property value.
func unmarshalPropertyMap(ctx *Context, v resource.PropertyMap) (Map, error) {
	if v == nil {
		return nil, nil
	}

	var unmarshal func(resource.PropertyValue) (Input, error)
	unmarshal = func(v resource.PropertyValue) (Input, error) {
		switch {
		case v.IsNull():
			return nil, nil
		case v.IsBool():
			return Bool(v.BoolValue()), nil
		case v.IsNumber():
			return Float64(v.NumberValue()), nil
		case v.IsString():
			return String(v.StringValue()), nil
		case v.IsArray():
			a := v.ArrayValue()
			r := make(Array, len(a))
			for i, v := range a {
				uv, err := unmarshal(v)
				if err != nil {
					return nil, err
				}
				r[i] = uv
			}
			return r, nil
		case v.IsObject():
			m := v.ObjectValue()
			return unmarshalPropertyMap(ctx, m)
		case v.IsAsset():
			asset := v.AssetValue()
			switch {
			case asset.IsPath():
				return NewFileAsset(asset.Path), nil
			case asset.IsText():
				return NewStringAsset(asset.Text), nil
			case asset.IsURI():
				return NewRemoteAsset(asset.URI), nil
			}
			return nil, errors.New("expected asset to be one of File, String, or Remote; got none")
		case v.IsArchive():
			archive := v.ArchiveValue()
			secret := false
			switch {
			case archive.IsAssets():
				as := make(map[string]interface{})
				for k, v := range archive.Assets {
					a, asecret, err := unmarshalPropertyValue(ctx, resource.NewPropertyValue(v))
					secret = secret || asecret
					if err != nil {
						return nil, err
					}
					as[k] = a
				}
				return NewAssetArchive(as), nil
			case archive.IsPath():
				return NewFileArchive(archive.Path), nil
			case archive.IsURI():
				return NewRemoteArchive(archive.URI), nil
			}
			return nil, errors.New("expected archive to be one of Assets, File, or Remote; got none")
		case v.IsResourceReference():
			resRef := v.ResourceReferenceValue()
			res := ctx.newDependencyResource(URN(resRef.URN))

			output := ctx.newOutput(reflect.TypeOf((*ResourceOutput)(nil)).Elem())
			internal.ResolveOutput(output, res, true, false, nil /* deps */)
			return output, nil

		case v.IsComputed():
			typ := reflect.TypeOf((*any)(nil)).Elem()
			typ = getOutputType(typ)
			output := ctx.newOutput(typ)
			internal.ResolveOutput(output, nil, false, false, nil /* deps */)
			return output, nil
		case v.IsSecret():
			element, err := unmarshal(v.SecretValue().Element)
			if err != nil {
				return nil, err
			}
			return ToSecret(element), nil
		case v.IsOutput():
			deps := make([]internal.Resource, len(v.OutputValue().Dependencies))
			for i, dep := range v.OutputValue().Dependencies {
				deps[i] = ctx.newDependencyResource(URN(dep))
			}

			known := v.OutputValue().Known
			secret := v.OutputValue().Secret

			// If the output is known, we can unmarshal it directly else it's nil
			typ := anyOutputType
			var element interface{}
			if v.OutputValue().Known {
				var err error
				element, err = unmarshal(v.OutputValue().Element)
				if err != nil {
					return nil, err
				}

				// Return an output of the type of the inner value, except for nil which should type as Output[any].
				if element != nil {
					// element will be an Input/Output type like pulumi.String or pulumi.AnyOutput. We want
					// the inner value to assign to the output below. If the inner value is an output itself
					// this collapses it to a single output value, this probably isn't ideal but nested
					// outputs are really hard to support wihout generics.
					o := ToOutput(element)
					if o != nil {
						typ = reflect.TypeOf(o)

						innerValue, innerKnown, innerSecret, innerDeps, err := awaitWithContext(ctx.Context(), o)
						if err != nil {
							return nil, err
						}
						element = innerValue
						known = known && innerKnown
						secret = secret || innerSecret
						for _, dep := range innerDeps {
							deps = append(deps, dep)
						}
					}
				}
			}

			output := ctx.newOutput(typ)
			internal.ResolveOutput(output, element, known, secret, deps)
			return output, nil
		}

		return nil, fmt.Errorf("unknown property value %v", v)
	}

	m := make(Map)
	for k, v := range v {
		uv, err := unmarshal(v)
		if err != nil {
			return nil, err
		}
		m[string(k)] = uv
	}
	return m, nil
}

// unmarshalOutput unmarshals a single output variable into its runtime representation.
// returning a bool that indicates secretness
func unmarshalOutput(ctx *Context, v resource.PropertyValue, dest reflect.Value) (bool, error) {
	contract.Requiref(dest.CanSet(), "dest", "value must be settable")

	// Check for nils and unknowns. The destination will be left with the zero value.
	if v.IsNull() || v.IsComputed() || (v.IsOutput() && !v.OutputValue().Known) {
		return false, nil
	}

	allocatedPointer := false
	// Allocate storage as necessary.
	for dest.Kind() == reflect.Ptr {
		allocatedPointer = true
		elem := reflect.New(dest.Type().Elem())
		dest.Set(elem)
		dest = elem.Elem()
	}

	// In the case of assets and archives, turn these into real asset and archive structures.
	switch {
	case v.IsAsset():
		if !assetType.AssignableTo(dest.Type()) {
			return false, fmt.Errorf("expected a %s, got an asset", dest.Type())
		}

		asset, secret, err := unmarshalPropertyValue(ctx, v)
		if err != nil {
			return false, err
		}
		dest.Set(reflect.ValueOf(asset))
		return secret, nil
	case v.IsArchive():
		if !archiveType.AssignableTo(dest.Type()) {
			return false, fmt.Errorf("expected a %s, got an archive", dest.Type())
		}

		archive, secret, err := unmarshalPropertyValue(ctx, v)
		if err != nil {
			return false, err
		}
		dest.Set(reflect.ValueOf(archive))
		return secret, nil
	case v.IsSecret():
		if _, err := unmarshalOutput(ctx, v.SecretValue().Element, dest); err != nil {
			return false, err
		}
		return true, nil
	case v.IsResourceReference():
		res, secret, err := unmarshalPropertyValue(ctx, v)
		if err != nil {
			return false, err
		}
		resV := reflect.ValueOf(res)
		// If we unmarshal a pointer and the destination is "any", we also want to make sure the result is a
		// pointer.  We check above whether the destination is a pointer, but that's not true for "any", even
		// though it can hold a pointer.
		if !allocatedPointer && resV.Kind() == reflect.Ptr && dest.Type().Kind() == reflect.Interface &&
			resV.Elem().Type().AssignableTo(dest.Type()) {
			dest.Set(resV)
			return secret, nil
		}

		if !resV.Elem().Type().AssignableTo(dest.Type()) {
			return false, fmt.Errorf("expected a %s, got a resource of type %s", dest.Type(), resV.Type())
		}
		dest.Set(resV.Elem())
		return secret, nil
	case v.IsOutput():
		if _, err := unmarshalOutput(ctx, v.OutputValue().Element, dest); err != nil {
			return false, err
		}
		return v.OutputValue().Secret, nil
	}

	// Unmarshal based on the desired type.
	//nolint:exhaustive // We only need to handle a few types here.
	switch dest.Kind() {
	case reflect.Bool:
		if !v.IsBool() {
			return false, fmt.Errorf("expected a %v, got a %s", dest.Type(), v.TypeString())
		}
		dest.SetBool(v.BoolValue())
		return false, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if !v.IsNumber() {
			return false, fmt.Errorf("expected an %v, got a %s", dest.Type(), v.TypeString())
		}
		dest.SetInt(int64(v.NumberValue()))
		return false, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if !v.IsNumber() {
			return false, fmt.Errorf("expected an %v, got a %s", dest.Type(), v.TypeString())
		}
		dest.SetUint(uint64(v.NumberValue()))
		return false, nil
	case reflect.Float32, reflect.Float64:
		if !v.IsNumber() {
			return false, fmt.Errorf("expected an %v, got a %s", dest.Type(), v.TypeString())
		}
		dest.SetFloat(v.NumberValue())
		return false, nil
	case reflect.String:
		switch {
		case v.IsString():
			dest.SetString(v.StringValue())
		case v.IsResourceReference():
			ref := v.ResourceReferenceValue()
			if id, hasID := ref.IDString(); hasID {
				dest.SetString(id)
			} else {
				dest.SetString(string(ref.URN))
			}
		default:
			return false, fmt.Errorf("expected a %v, got a %s", dest.Type(), v.TypeString())
		}
		return false, nil
	case reflect.Slice:
		if !v.IsArray() {
			return false, fmt.Errorf("expected a %v, got a %s", dest.Type(), v.TypeString())
		}
		arr := v.ArrayValue()
		slice := reflect.MakeSlice(dest.Type(), len(arr), len(arr))
		secret := false
		for i, e := range arr {
			isecret, err := unmarshalOutput(ctx, e, slice.Index(i))
			if err != nil {
				return false, err
			}
			secret = secret || isecret
		}
		dest.Set(slice)
		return secret, nil
	case reflect.Map:
		if !v.IsObject() {
			return false, fmt.Errorf("expected a %v, got a %s", dest.Type(), v.TypeString())
		}

		keyType, elemType := dest.Type().Key(), dest.Type().Elem()
		if keyType.Kind() != reflect.String {
			return false, errors.New("map keys must be assignable from type string")
		}

		result := reflect.MakeMap(dest.Type())
		secret := false
		for k, e := range v.ObjectValue() {
			if resource.IsInternalPropertyKey(k) {
				continue
			}
			elem := reflect.New(elemType).Elem()
			esecret, err := unmarshalOutput(ctx, e, elem)
			if err != nil {
				return false, err
			}
			secret = secret || esecret

			key := reflect.New(keyType).Elem()
			key.SetString(string(k))

			result.SetMapIndex(key, elem)
		}
		dest.Set(result)
		return secret, nil
	case reflect.Interface:
		// Tolerate invalid asset or archive values.
		typ := dest.Type()
		switch typ {
		case assetType:
			_, secret, err := unmarshalPropertyValue(ctx, v)
			if err != nil {
				return false, err
			}
			asset := &asset{invalid: true}
			dest.Set(reflect.ValueOf(asset))
			return secret, nil
		case archiveType:
			_, secret, err := unmarshalPropertyValue(ctx, v)
			if err != nil {
				return false, err
			}
			archive := &archive{invalid: true}
			dest.Set(reflect.ValueOf(archive))
			return secret, nil
		}

		if !anyType.Implements(typ) {
			return false, fmt.Errorf("cannot unmarshal into non-empty interface type %v", dest.Type())
		}

		// If we're unmarshaling into the empty interface type, use the property type as the type of the result.
		result, secret, err := unmarshalPropertyValue(ctx, v)
		if err != nil {
			return false, err
		}
		dest.Set(reflect.ValueOf(result))
		return secret, nil
	case reflect.Struct:
		typ := dest.Type()
		if !v.IsObject() {
			return false, fmt.Errorf("expected a %v, got a %s", dest.Type(), v.TypeString())
		}

		obj := v.ObjectValue()
		secret := false
		for i := 0; i < typ.NumField(); i++ {
			fieldV := dest.Field(i)
			if !fieldV.CanSet() {
				continue
			}

			tag := typ.Field(i).Tag.Get("pulumi")
			tag = strings.Split(tag, ",")[0] // tagName,flag => tagName
			if tag == "" {
				continue
			}

			e, ok := obj[resource.PropertyKey(tag)]
			if !ok {
				continue
			}

			osecret, err := unmarshalOutput(ctx, e, fieldV)
			secret = secret || osecret
			if err != nil {
				return false, err
			}
		}
		return secret, nil
	default:
		return false, fmt.Errorf("cannot unmarshal into type %v", dest.Type())
	}
}

type Versioned interface {
	Version() semver.Version
}

type versionedMap struct {
	sync.RWMutex
	versions map[string][]Versioned
}

// nullVersion represents the wildcard version (match any version).
var nullVersion semver.Version

func (vm *versionedMap) Load(key string, version semver.Version) (Versioned, bool) {
	vm.RLock()
	defer vm.RUnlock()

	wildcard := version.EQ(nullVersion)

	var bestVersion Versioned
	for _, v := range vm.versions[key] {
		// Unless we are matching a wildcard version, constrain search to matching major version.
		if !wildcard && v.Version().Major != version.Major {
			continue
		}

		// If we find an exact match, return that.
		if v.Version().EQ(version) {
			return v, true
		}

		if bestVersion == nil {
			bestVersion = v
			continue
		}
		if v.Version().GTE(bestVersion.Version()) {
			bestVersion = v
		}
	}

	return bestVersion, bestVersion != nil
}

func (vm *versionedMap) Store(key string, value Versioned) error {
	vm.Lock()
	defer vm.Unlock()

	hasVersion := func(versions []Versioned, version semver.Version) bool {
		for _, v := range versions {
			if v.Version().EQ(value.Version()) {
				return true
			}
		}
		return false
	}

	if _, exists := vm.versions[key]; exists && hasVersion(vm.versions[key], value.Version()) {
		return fmt.Errorf("existing registration for %v: %s", key, value.Version())
	}

	vm.versions[key] = append(vm.versions[key], value)

	return nil
}

type ResourcePackage interface {
	Versioned
	ConstructProvider(ctx *Context, name, typ, urn string) (ProviderResource, error)
}

type ResourceModule interface {
	Versioned
	Construct(ctx *Context, name, typ, urn string) (Resource, error)
}

var (
	resourcePackages versionedMap
	resourceModules  versionedMap
)

// RegisterResourcePackage register a resource package with the Pulumi runtime.
func RegisterResourcePackage(pkg string, resourcePackage ResourcePackage) {
	if err := resourcePackages.Store(pkg, resourcePackage); err != nil {
		panic(err)
	}
}

func moduleKey(pkg, mod string) string {
	return fmt.Sprintf("%s:%s", pkg, mod)
}

// RegisterResourceModule register a resource module with the Pulumi runtime.
func RegisterResourceModule(pkg, mod string, module ResourceModule) {
	key := moduleKey(pkg, mod)
	if err := resourceModules.Store(key, module); err != nil {
		panic(err)
	}
}

func init() {
	resourcePackages = versionedMap{versions: make(map[string][]Versioned)}
	resourceModules = versionedMap{versions: make(map[string][]Versioned)}
}

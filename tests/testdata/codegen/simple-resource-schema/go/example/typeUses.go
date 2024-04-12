// Code generated by test DO NOT EDIT.
// *** WARNING: Do not edit by hand unless you're certain you know what you are doing! ***

package example

import (
	"context"
	"reflect"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"simple-resource-schema/example/internal"
)

type TypeUses struct {
	pulumi.CustomResourceState

	Bar SomeOtherObjectPtrOutput              `pulumi:"bar"`
	Baz ObjectWithNodeOptionalInputsPtrOutput `pulumi:"baz"`
	Foo ObjectPtrOutput                       `pulumi:"foo"`
}

// NewTypeUses registers a new resource with the given unique name, arguments, and options.
func NewTypeUses(ctx *pulumi.Context,
	name string, args *TypeUsesArgs, opts ...pulumi.ResourceOption) (*TypeUses, error) {
	if args == nil {
		args = &TypeUsesArgs{}
	}

	opts = internal.PkgResourceDefaultOpts(opts)
	var resource TypeUses
	err := ctx.RegisterResource("example::TypeUses", name, args, &resource, opts...)
	if err != nil {
		return nil, err
	}
	return &resource, nil
}

// GetTypeUses gets an existing TypeUses resource's state with the given name, ID, and optional
// state properties that are used to uniquely qualify the lookup (nil if not required).
func GetTypeUses(ctx *pulumi.Context,
	name string, id pulumi.IDInput, state *TypeUsesState, opts ...pulumi.ResourceOption) (*TypeUses, error) {
	var resource TypeUses
	err := ctx.ReadResource("example::TypeUses", name, id, state, &resource, opts...)
	if err != nil {
		return nil, err
	}
	return &resource, nil
}

// Input properties used for looking up and filtering TypeUses resources.
type typeUsesState struct {
}

type TypeUsesState struct {
}

func (TypeUsesState) ElementType() reflect.Type {
	return reflect.TypeOf((*typeUsesState)(nil)).Elem()
}

type typeUsesArgs struct {
	Bar *SomeOtherObject              `pulumi:"bar"`
	Baz *ObjectWithNodeOptionalInputs `pulumi:"baz"`
	Foo *Object                       `pulumi:"foo"`
}

// The set of arguments for constructing a TypeUses resource.
type TypeUsesArgs struct {
	Bar SomeOtherObjectPtrInput
	Baz ObjectWithNodeOptionalInputsPtrInput
	Foo ObjectPtrInput
}

func (TypeUsesArgs) ElementType() reflect.Type {
	return reflect.TypeOf((*typeUsesArgs)(nil)).Elem()
}

type TypeUsesInput interface {
	pulumi.Input

	ToTypeUsesOutput() TypeUsesOutput
	ToTypeUsesOutputWithContext(ctx context.Context) TypeUsesOutput
}

func (*TypeUses) ElementType() reflect.Type {
	return reflect.TypeOf((**TypeUses)(nil)).Elem()
}

func (i *TypeUses) ToTypeUsesOutput() TypeUsesOutput {
	return i.ToTypeUsesOutputWithContext(context.Background())
}

func (i *TypeUses) ToTypeUsesOutputWithContext(ctx context.Context) TypeUsesOutput {
	return pulumi.ToOutputWithContext(ctx, i).(TypeUsesOutput)
}

type TypeUsesOutput struct{ *pulumi.OutputState }

func (TypeUsesOutput) ElementType() reflect.Type {
	return reflect.TypeOf((**TypeUses)(nil)).Elem()
}

func (o TypeUsesOutput) ToTypeUsesOutput() TypeUsesOutput {
	return o
}

func (o TypeUsesOutput) ToTypeUsesOutputWithContext(ctx context.Context) TypeUsesOutput {
	return o
}

func (o TypeUsesOutput) Bar() SomeOtherObjectPtrOutput {
	return o.ApplyT(func(v *TypeUses) SomeOtherObjectPtrOutput { return v.Bar }).(SomeOtherObjectPtrOutput)
}

func (o TypeUsesOutput) Baz() ObjectWithNodeOptionalInputsPtrOutput {
	return o.ApplyT(func(v *TypeUses) ObjectWithNodeOptionalInputsPtrOutput { return v.Baz }).(ObjectWithNodeOptionalInputsPtrOutput)
}

func (o TypeUsesOutput) Foo() ObjectPtrOutput {
	return o.ApplyT(func(v *TypeUses) ObjectPtrOutput { return v.Foo }).(ObjectPtrOutput)
}

func init() {
	pulumi.RegisterInputType(reflect.TypeOf((*TypeUsesInput)(nil)).Elem(), &TypeUses{})
	pulumi.RegisterOutputType(TypeUsesOutput{})
}
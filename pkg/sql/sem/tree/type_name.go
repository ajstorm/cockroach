// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package tree

import (
	"context"
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgcode"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgerror"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/errors"
	"github.com/lib/pq/oid"
)

// TypeName corresponds to the name of a type in a CREATE TYPE statement,
// in an expression, or column type etc.
//
// Users of this struct should not construct it directly, and
// instead use the constructors below.
type TypeName struct {
	objName
}

// Satisfy the linter.
var _ = (*TypeName).Type
var _ = (*TypeName).FQString
var _ = NewUnqualifiedTypeName

// Type returns the unqualified name of this TypeName.
func (t *TypeName) Type() string {
	return string(t.ObjectName)
}

// Format implements the NodeFormatter interface.
func (t *TypeName) Format(ctx *FmtCtx) {
	t.ObjectNamePrefix.Format(ctx)
	if t.ExplicitSchema || ctx.alwaysFormatTablePrefix() {
		ctx.WriteByte('.')
	}
	ctx.FormatNode(&t.ObjectName)
}

// String implements the Stringer interface.
func (t *TypeName) String() string {
	return AsString(t)
}

// FQString renders the type name in full, not omitting the prefix
// schema and catalog names. Suitable for logging, etc.
func (t *TypeName) FQString() string {
	ctx := NewFmtCtx(FmtSimple)
	ctx.FormatNode(&t.CatalogName)
	ctx.WriteByte('.')
	ctx.FormatNode(&t.SchemaName)
	ctx.WriteByte('.')
	ctx.FormatNode(&t.ObjectName)
	return ctx.CloseAndGetString()
}

func (t *TypeName) objectName() {}

// NewUnqualifiedTypeName returns a new base type name.
func NewUnqualifiedTypeName(typ Name) *TypeName {
	return &TypeName{objName{
		ObjectName: typ,
	}}
}

// MakeUnqualifiedTypeName returns a new type name.
func MakeUnqualifiedTypeName(typ Name) TypeName {
	return TypeName{objName{
		ObjectName: typ,
	}}
}

// MakeNewQualifiedTypeName creates a fully qualified type name.
func MakeNewQualifiedTypeName(db, schema, typ string) TypeName {
	return TypeName{objName{
		ObjectNamePrefix: ObjectNamePrefix{
			ExplicitCatalog: true,
			ExplicitSchema:  true,
			CatalogName:     Name(db),
			SchemaName:      Name(schema),
		},
		ObjectName: Name(typ),
	}}
}

// NewQualifiedTypeName returns a fully qualified type name.
func NewQualifiedTypeName(db, schema, typ string) *TypeName {
	return &TypeName{objName{
		ObjectNamePrefix: ObjectNamePrefix{
			ExplicitCatalog: true,
			ExplicitSchema:  true,
			CatalogName:     Name(db),
			SchemaName:      Name(schema),
		},
		ObjectName: Name(typ),
	}}
}

// TypeReferenceResolver is the interface that will provide the ability
// to actually look up type metadata and transform references into
// *types.T's. Implementers of TypeReferenceResolver should also implement
// descpb.TypeDescriptorResolver is sqlbase.TypeDescriptorInterface is the
// underlying representation of a user defined type.
type TypeReferenceResolver interface {
	ResolveType(ctx context.Context, name *UnresolvedObjectName) (*types.T, error)
	ResolveTypeByOID(ctx context.Context, oid oid.Oid) (*types.T, error)
}

// ResolvableTypeReference represents a type that is possibly unknown
// until type-checking/type name resolution is performed.
// N.B. ResolvableTypeReferences in expressions must be formatted with
// FormatTypeReference instead of SQLString.
type ResolvableTypeReference interface {
	SQLString() string
}

var _ ResolvableTypeReference = &UnresolvedObjectName{}
var _ ResolvableTypeReference = &ArrayTypeReference{}
var _ ResolvableTypeReference = &types.T{}
var _ ResolvableTypeReference = &OIDTypeReference{}

// ResolveType converts a ResolvableTypeReference into a *types.T.
func ResolveType(
	ctx context.Context, ref ResolvableTypeReference, resolver TypeReferenceResolver,
) (*types.T, error) {
	switch t := ref.(type) {
	case *types.T:
		return t, nil
	case *ArrayTypeReference:
		typ, err := ResolveType(ctx, t.ElementType, resolver)
		if err != nil {
			return nil, err
		}
		return types.MakeArray(typ), nil
	case *UnresolvedObjectName:
		if resolver == nil {
			// If we don't have a resolver, we can't actually resolve this
			// name into a type.
			return nil, pgerror.Newf(pgcode.UndefinedObject, "type %q does not exist", t)
		}
		return resolver.ResolveType(ctx, t)
	case *OIDTypeReference:
		if resolver == nil {
			return nil, pgerror.Newf(pgcode.UndefinedObject, "type OID %d does not exist", t.OID)
		}
		return resolver.ResolveTypeByOID(ctx, t.OID)
	default:
		return nil, errors.AssertionFailedf("unknown resolvable type reference type %s", t)
	}
}

// FormatTypeReference formats a ResolvableTypeReference.
func (ctx *FmtCtx) FormatTypeReference(ref ResolvableTypeReference) {
	if ctx.HasFlags(fmtStaticallyFormatUserDefinedTypes) {
		switch t := ref.(type) {
		case *types.T:
			if t.UserDefined() {
				idRef := OIDTypeReference{OID: t.Oid()}
				ctx.WriteString(idRef.SQLString())
				return
			}
		}
	}
	if idRef, ok := ref.(*OIDTypeReference); ok && ctx.indexedTypeFormatter != nil {
		ctx.indexedTypeFormatter(ctx, idRef)
		return
	}
	ctx.WriteString(ref.SQLString())
}

// GetStaticallyKnownType possibly promotes a ResolvableTypeReference into a
// *types.T if the reference is a statically known type. It is only safe to
// access the returned type if ok is true.
func GetStaticallyKnownType(ref ResolvableTypeReference) (typ *types.T, ok bool) {
	typ, ok = ref.(*types.T)
	return typ, ok
}

// MustBeStaticallyKnownType does the same thing as GetStaticallyKnownType but panics
// in the case that the reference is not statically known. This function
// is intended to be used in tests or in cases where it is not possible
// to have any unresolved type references.
func MustBeStaticallyKnownType(ref ResolvableTypeReference) *types.T {
	if typ, ok := ref.(*types.T); ok {
		return typ
	}
	panic(errors.AssertionFailedf("type reference was not a statically known type"))
}

// OIDTypeReference is a reference to a type directly by its stable ID.
type OIDTypeReference struct {
	OID oid.Oid
}

// SQLString implements the ResolvableTypeReference interface.
func (node *OIDTypeReference) SQLString() string {
	return fmt.Sprintf("@%d", node.OID)
}

// ArrayTypeReference represents an array of possibly unknown type references.
type ArrayTypeReference struct {
	ElementType ResolvableTypeReference
}

// SQLString implements the ResolvableTypeReference interface.
func (node *ArrayTypeReference) SQLString() string {
	var ctx FmtCtx
	if typ, ok := GetStaticallyKnownType(node.ElementType); ok {
		ctx.WriteString(types.MakeArray(typ).SQLString())
	} else {
		ctx.WriteString(node.ElementType.SQLString())
		ctx.WriteString("[]")
	}
	return ctx.String()
}

// SQLString implements the ResolvableTypeReference interface.
func (name *UnresolvedObjectName) SQLString() string {
	return name.String()
}

// IsReferenceSerialType returns whether the input reference is a known
// serial type. It should only be used during parsing.
func IsReferenceSerialType(ref ResolvableTypeReference) bool {
	if typ, ok := GetStaticallyKnownType(ref); ok {
		return types.IsSerialType(typ)
	}
	return false
}

// TypeCollectorVisitor is an expression visitor that collects all explicit
// OID type references in an expression.
type TypeCollectorVisitor struct {
	OIDs map[oid.Oid]struct{}
}

// VisitPre implements the Visitor interface.
func (v *TypeCollectorVisitor) VisitPre(expr Expr) (bool, Expr) {
	switch t := expr.(type) {
	case Datum:
		if t.ResolvedType().UserDefined() {
			v.OIDs[t.ResolvedType().Oid()] = struct{}{}
		}
	case *IsOfTypeExpr:
		for _, ref := range t.Types {
			if idref, ok := ref.(*OIDTypeReference); ok {
				v.OIDs[idref.OID] = struct{}{}
			}
		}
	case *CastExpr:
		if idref, ok := t.Type.(*OIDTypeReference); ok {
			v.OIDs[idref.OID] = struct{}{}
		}
	case *AnnotateTypeExpr:
		if idref, ok := t.Type.(*OIDTypeReference); ok {
			v.OIDs[idref.OID] = struct{}{}
		}
	}
	return true, expr
}

// VisitPost implements the Visitor interface.
func (v *TypeCollectorVisitor) VisitPost(e Expr) Expr {
	return e
}

// TestingMapTypeResolver is a fake type resolver for testing purposes.
type TestingMapTypeResolver struct {
	typeMap map[string]*types.T
}

// ResolveType implements the TypeReferenceResolver interface.
func (dtr *TestingMapTypeResolver) ResolveType(
	_ context.Context, name *UnresolvedObjectName,
) (*types.T, error) {
	typ, ok := dtr.typeMap[name.String()]
	if !ok {
		return nil, errors.Newf("type %q does not exist", name)
	}
	return typ, nil
}

// ResolveTypeByOID implements the TypeReferenceResolver interface.
func (dtr *TestingMapTypeResolver) ResolveTypeByOID(context.Context, oid.Oid) (*types.T, error) {
	return nil, errors.AssertionFailedf("unimplemented")
}

// MakeTestingMapTypeResolver creates a TestingMapTypeResolver from a map.
func MakeTestingMapTypeResolver(typeMap map[string]*types.T) TypeReferenceResolver {
	return &TestingMapTypeResolver{typeMap: typeMap}
}

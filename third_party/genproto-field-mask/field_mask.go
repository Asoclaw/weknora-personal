// Package field_mask preserves the legacy import used by grpc-gateway v1.
// The alias keeps the canonical protobuf type and wire representation.
package field_mask

import "google.golang.org/protobuf/types/known/fieldmaskpb"

type FieldMask = fieldmaskpb.FieldMask

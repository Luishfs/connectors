package boilerplate

import (
	"embed"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bradleyjkemp/cupaloy"
	pf "github.com/estuary/flow/go/protocols/flow"
	pm "github.com/estuary/flow/go/protocols/materialize"
	"github.com/stretchr/testify/require"
)

//go:generate ./testdata/generate-spec-proto.sh testdata/validate/base.flow.yaml
//go:generate ./testdata/generate-spec-proto.sh testdata/validate/incompatible-changes.flow.yaml
//go:generate ./testdata/generate-spec-proto.sh testdata/validate/fewer-fields.flow.yaml
//go:generate ./testdata/generate-spec-proto.sh testdata/validate/alternate-root.flow.yaml
//go:generate ./testdata/generate-spec-proto.sh testdata/validate/increment-backfill.flow.yaml

//go:embed testdata/validate/generated_specs
var validateFS embed.FS

func loadValidateSpec(t *testing.T, path string) *pf.MaterializationSpec {
	t.Helper()

	specBytes, err := validateFS.ReadFile(filepath.Join("testdata/validate/generated_specs", path))
	require.NoError(t, err)
	var spec pf.MaterializationSpec
	require.NoError(t, spec.Unmarshal(specBytes))

	return &spec
}

func TestValidate(t *testing.T) {
	type testCase struct {
		name              string
		deltaUpdates      bool
		specForInfoSchema *pf.MaterializationSpec
		existingSpec      *pf.MaterializationSpec
		proposedSpec      *pf.MaterializationSpec
	}

	tests := []testCase{
		{
			name:              "new materialization - standard updates",
			deltaUpdates:      false,
			specForInfoSchema: nil,
			existingSpec:      nil,
			proposedSpec:      loadValidateSpec(t, "base.flow.proto"),
		},
		{
			name:              "same binding again - standard updates",
			deltaUpdates:      false,
			specForInfoSchema: loadValidateSpec(t, "base.flow.proto"),
			existingSpec:      loadValidateSpec(t, "base.flow.proto"),
			proposedSpec:      loadValidateSpec(t, "base.flow.proto"),
		},
		{
			name:              "new materialization - delta updates",
			deltaUpdates:      true,
			specForInfoSchema: nil,
			existingSpec:      nil,
			proposedSpec:      loadValidateSpec(t, "base.flow.proto"),
		},
		{
			name:              "same binding again - delta updates",
			deltaUpdates:      true,
			specForInfoSchema: loadValidateSpec(t, "base.flow.proto"),
			existingSpec:      loadValidateSpec(t, "base.flow.proto"),
			proposedSpec:      loadValidateSpec(t, "base.flow.proto"),
		},
		{
			name:              "binding update with incompatible changes",
			deltaUpdates:      false,
			specForInfoSchema: loadValidateSpec(t, "base.flow.proto"),
			existingSpec:      loadValidateSpec(t, "base.flow.proto"),
			proposedSpec:      loadValidateSpec(t, "incompatible-changes.flow.proto"),
		},
		{
			name:              "fields exist in destination but not in collection",
			deltaUpdates:      false,
			specForInfoSchema: loadValidateSpec(t, "base.flow.proto"),
			existingSpec:      loadValidateSpec(t, "base.flow.proto"),
			proposedSpec:      loadValidateSpec(t, "fewer-fields.flow.proto"),
		},
		{
			name:              "change root document projection for standard updates",
			deltaUpdates:      false,
			specForInfoSchema: loadValidateSpec(t, "base.flow.proto"),
			existingSpec:      loadValidateSpec(t, "base.flow.proto"),
			proposedSpec:      loadValidateSpec(t, "alternate-root.flow.proto"),
		},
		{
			name:              "change root document projection for delta updates",
			deltaUpdates:      true,
			specForInfoSchema: loadValidateSpec(t, "base.flow.proto"),
			existingSpec:      loadValidateSpec(t, "base.flow.proto"),
			proposedSpec:      loadValidateSpec(t, "alternate-root.flow.proto"),
		},
		{
			name:              "increment backfill counter",
			deltaUpdates:      false,
			specForInfoSchema: loadValidateSpec(t, "base.flow.proto"),
			existingSpec:      loadValidateSpec(t, "base.flow.proto"),
			proposedSpec:      loadValidateSpec(t, "increment-backfill.flow.proto"),
		},
		{
			name:              "table already exists with identical spec",
			deltaUpdates:      false,
			specForInfoSchema: loadValidateSpec(t, "base.flow.proto"),
			existingSpec:      nil,
			proposedSpec:      loadValidateSpec(t, "base.flow.proto"),
		},
		{
			name:              "table already exists with incompatible proposed spec",
			deltaUpdates:      false,
			specForInfoSchema: loadValidateSpec(t, "base.flow.proto"),
			existingSpec:      nil,
			proposedSpec:      loadValidateSpec(t, "incompatible-changes.flow.proto"),
		},
	}

	var snap strings.Builder
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := testInfoSchemaFromSpec(t, tt.specForInfoSchema)
			validator := NewValidator(testConstrainter{}, is)

			cs, err := validator.ValidateBinding(
				[]string{"key_value"},
				tt.deltaUpdates,
				tt.proposedSpec.Bindings[0].Backfill,
				tt.proposedSpec.Bindings[0].Collection,
				tt.proposedSpec.Bindings[0].FieldSelection.FieldConfigJsonMap,
				tt.existingSpec,
			)
			require.NoError(t, err)

			j, err := json.MarshalIndent(cs, "", "\t")
			require.NoError(t, err)

			snap.WriteString("--- Begin " + tt.name + " ---\n")
			snap.WriteString(string(j))
			snap.WriteString("\n--- End " + tt.name + " ---\n\n")
		})
	}
	cupaloy.SnapshotT(t, snap.String())

	t.Run("can't decrement backfill counter", func(t *testing.T) {
		existing := loadValidateSpec(t, "increment-backfill.flow.proto")
		proposed := loadValidateSpec(t, "base.flow.proto")
		is := testInfoSchemaFromSpec(t, existing)
		validator := NewValidator(testConstrainter{}, is)

		_, err := validator.ValidateBinding(
			[]string{"key_value"},
			false,
			proposed.Bindings[0].Backfill,
			proposed.Bindings[0].Collection,
			proposed.Bindings[0].FieldSelection.FieldConfigJsonMap,
			existing,
		)

		require.ErrorContains(t, err, "backfill count 0 is less than previously applied count of 1")
	})

	t.Run("can't switch from delta to standard updates", func(t *testing.T) {
		existing := loadValidateSpec(t, "base.flow.proto")
		proposed := loadValidateSpec(t, "base.flow.proto")

		existing.Bindings[0].DeltaUpdates = true

		is := testInfoSchemaFromSpec(t, existing)
		validator := NewValidator(testConstrainter{}, is)

		_, err := validator.ValidateBinding(
			[]string{"key_value"},
			false,
			proposed.Bindings[0].Backfill,
			proposed.Bindings[0].Collection,
			proposed.Bindings[0].FieldSelection.FieldConfigJsonMap,
			existing,
		)

		require.ErrorContains(t, err, "changing from delta updates to standard updates is not allowed")
	})

	t.Run("can switch from delta updates to standard updates", func(t *testing.T) {
		existing := loadValidateSpec(t, "base.flow.proto")
		proposed := loadValidateSpec(t, "base.flow.proto")

		proposed.Bindings[0].DeltaUpdates = true

		is := testInfoSchemaFromSpec(t, existing)
		validator := NewValidator(testConstrainter{}, is)

		_, err := validator.ValidateBinding(
			[]string{"key_value"},
			true,
			proposed.Bindings[0].Backfill,
			proposed.Bindings[0].Collection,
			proposed.Bindings[0].FieldSelection.FieldConfigJsonMap,
			existing,
		)

		require.NoError(t, err)
	})

	t.Run("can't materialize two collections to the same target", func(t *testing.T) {
		existing := loadValidateSpec(t, "base.flow.proto")
		proposed := loadValidateSpec(t, "base.flow.proto")

		proposed.Bindings[0].Collection.Name = pf.Collection("other")

		is := testInfoSchemaFromSpec(t, existing)
		validator := NewValidator(testConstrainter{}, is)

		_, err := validator.ValidateBinding(
			[]string{"key_value"},
			false,
			proposed.Bindings[0].Backfill,
			proposed.Bindings[0].Collection,
			proposed.Bindings[0].FieldSelection.FieldConfigJsonMap,
			existing,
		)

		require.ErrorContains(t, err, "cannot add a new binding to materialize collection 'other' to '[key_value]' because an existing binding for collection 'key/value' is already materializing to '[key_value]'")
	})
}

func TestAsFormattedNumeric(t *testing.T) {
	tests := []struct {
		name         string
		inference    pf.Inference
		isPrimaryKey bool
		want         StringWithNumericFormat
	}{
		{
			name: "integer formatted string",
			inference: pf.Inference{
				Exists: pf.Inference_MUST,
				Types:  []string{"string"},
				String_: &pf.Inference_String{
					Format: "integer",
				},
			},
			isPrimaryKey: false,
			want:         StringFormatInteger,
		},
		{
			name: "number formatted string",
			inference: pf.Inference{
				Exists: pf.Inference_MAY,
				Types:  []string{"string"},
				String_: &pf.Inference_String{
					Format: "number",
				},
			},
			isPrimaryKey: false,
			want:         StringFormatNumber,
		},
		{
			name: "nullable integer formatted string",
			inference: pf.Inference{
				Exists: pf.Inference_MUST,
				Types:  []string{"null", "string"},
				String_: &pf.Inference_String{
					Format: "integer",
				},
			},
			isPrimaryKey: false,
			want:         StringFormatInteger,
		},
		{
			name: "nullable number formatted string",
			inference: pf.Inference{
				Exists: pf.Inference_MAY,
				Types:  []string{"null", "string"},
				String_: &pf.Inference_String{
					Format: "number",
				},
			},
			isPrimaryKey: false,
			want:         StringFormatNumber,
		},
		{
			name: "integer formatted string with integer",
			inference: pf.Inference{
				Exists: pf.Inference_MUST,
				Types:  []string{"integer", "string"},
				String_: &pf.Inference_String{
					Format: "integer",
				},
			},
			isPrimaryKey: false,
			want:         StringFormatInteger,
		},
		{
			name: "number formatted string with number",
			inference: pf.Inference{
				Exists: pf.Inference_MAY,
				Types:  []string{"number", "string"},
				String_: &pf.Inference_String{
					Format: "number",
				},
			},
			isPrimaryKey: false,
			want:         StringFormatNumber,
		},
		{
			name: "nullable integer formatted string with integer",
			inference: pf.Inference{
				Exists: pf.Inference_MUST,
				Types:  []string{"integer", "null", "string"},
				String_: &pf.Inference_String{
					Format: "integer",
				},
			},
			isPrimaryKey: false,
			want:         StringFormatInteger,
		},
		{
			name: "nullable number formatted string with number",
			inference: pf.Inference{
				Exists: pf.Inference_MAY,
				Types:  []string{"null", "number", "string"},
				String_: &pf.Inference_String{
					Format: "number",
				},
			},
			isPrimaryKey: false,
			want:         StringFormatNumber,
		},
		{
			name: "doesn't apply to collection keys",
			inference: pf.Inference{
				Exists: pf.Inference_MUST,
				Types:  []string{"string"},
				String_: &pf.Inference_String{
					Format: "integer",
				},
			},
			isPrimaryKey: true,
			want:         "",
		},
		{
			name: "doesn't apply to other types",
			inference: pf.Inference{
				Exists: pf.Inference_MUST,
				Types:  []string{"object"},
			},
			isPrimaryKey: false,
			want:         "",
		},
		{
			name: "doesn't apply to strings with other formats",
			inference: pf.Inference{
				Exists: pf.Inference_MAY,
				Types:  []string{"null", "number", "string"},
				String_: &pf.Inference_String{
					Format: "base64",
				},
			},
			isPrimaryKey: false,
			want:         "",
		},
		{
			name: "doesn't apply if there are other types",
			inference: pf.Inference{
				Exists: pf.Inference_MUST,
				Types:  []string{"string", "object"},
				String_: &pf.Inference_String{
					Format: "integer",
				},
			},
			isPrimaryKey: false,
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatted, ok := AsFormattedNumeric(&pf.Projection{
				Inference:    tt.inference,
				IsPrimaryKey: tt.isPrimaryKey,
			})

			require.Equal(t, tt.want, formatted)
			if tt.want == "" {
				require.False(t, ok)
			} else {
				require.True(t, ok)
			}
		})
	}
}

func TestForbidAmbiguousFields(t *testing.T) {
	translate := func(in string) string {
		return strings.ToLower(in)
	}

	v := Validator{
		is: NewInfoSchema(
			func(in []string) []string {
				out := make([]string, 0, len(in))
				for _, i := range in {
					out = append(out, translate(i))
				}
				return out
			},
			translate,
		),
	}

	originalConstraints := map[string]*pm.Response_Validated_Constraint{
		"onlyOne":        {Type: pm.Response_Validated_Constraint_FIELD_OPTIONAL, Reason: "this is ok"},
		"notGood":        {Type: pm.Response_Validated_Constraint_FIELD_OPTIONAL, Reason: "shouldn't be allowed"},
		"NotGood":        {Type: pm.Response_Validated_Constraint_FIELD_OPTIONAL, Reason: "shouldn't be allowed"},
		"somethingelse":  {Type: pm.Response_Validated_Constraint_FIELD_OPTIONAL, Reason: "this is ok"},
		"something_else": {Type: pm.Response_Validated_Constraint_FIELD_OPTIONAL, Reason: "this is also ok"},
	}

	want := map[string]*pm.Response_Validated_Constraint{
		"onlyOne":        {Type: pm.Response_Validated_Constraint_FIELD_OPTIONAL, Reason: "this is ok"},
		"notGood":        {Type: pm.Response_Validated_Constraint_FIELD_FORBIDDEN, Reason: "Flow collection field 'notGood' would be materialized `notgood`, which is ambiguous with the materializations for other Flow collection fields [notGood,NotGood]. Consider using an alternate, unambiguous projection of this field to allow it to be materialized."},
		"NotGood":        {Type: pm.Response_Validated_Constraint_FIELD_FORBIDDEN, Reason: "Flow collection field 'NotGood' would be materialized `notgood`, which is ambiguous with the materializations for other Flow collection fields [notGood,NotGood]. Consider using an alternate, unambiguous projection of this field to allow it to be materialized."},
		"somethingelse":  {Type: pm.Response_Validated_Constraint_FIELD_OPTIONAL, Reason: "this is ok"},
		"something_else": {Type: pm.Response_Validated_Constraint_FIELD_OPTIONAL, Reason: "this is also ok"},
	}

	require.Equal(t, want, v.forbidAmbiguousFields(originalConstraints))
}

type testConstrainter struct{}

func (testConstrainter) Compatible(existing EndpointField, proposed *pf.Projection, _ json.RawMessage) (bool, error) {
	return existing.Type == strings.Join(proposed.Inference.Types, ","), nil
}

func (testConstrainter) DescriptionForType(p *pf.Projection) string {
	return strings.Join(p.Inference.Types, ", ")
}

func (testConstrainter) NewConstraints(p *pf.Projection, deltaUpdates bool) *pm.Response_Validated_Constraint {
	_, numericString := AsFormattedNumeric(p)

	var constraint = new(pm.Response_Validated_Constraint)
	switch {
	case p.IsPrimaryKey:
		constraint.Type = pm.Response_Validated_Constraint_LOCATION_REQUIRED
		constraint.Reason = "All Locations that are part of the collections key are required"
	case p.IsRootDocumentProjection() && deltaUpdates:
		constraint.Type = pm.Response_Validated_Constraint_LOCATION_RECOMMENDED
		constraint.Reason = "The root document should usually be materialized"
	case p.IsRootDocumentProjection():
		constraint.Type = pm.Response_Validated_Constraint_LOCATION_REQUIRED
		constraint.Reason = "The root document is required for a standard updates materialization"
	case p.Field == "locRequiredVal":
		constraint.Type = pm.Response_Validated_Constraint_LOCATION_REQUIRED
		constraint.Reason = "This location is required to be materialized"
	case p.Inference.IsSingleScalarType() || numericString:
		constraint.Type = pm.Response_Validated_Constraint_LOCATION_RECOMMENDED
		constraint.Reason = "The projection has a single scalar type"
	case reflect.DeepEqual(p.Inference.Types, []string{"null"}):
		constraint.Type = pm.Response_Validated_Constraint_FIELD_FORBIDDEN
		constraint.Reason = "Cannot materialize this field"

	default:
		constraint.Type = pm.Response_Validated_Constraint_FIELD_OPTIONAL
		constraint.Reason = "This field is able to be materialized"
	}
	return constraint
}
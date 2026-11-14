package stats

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	statsengine "github.com/haaku-tv/haaku/internal/stats"
)

// TestUserStatsSchemaMatchesPayload pins the hand-written OpenAPI schema to the
// struct the handler actually serialises. The schema is a map (so the generator
// emits exactly the contract's keys, including the distribution's numeric
// bucket names), which means the two could drift silently — this walks them
// together and fails on a field that exists in one and not the other.
func TestUserStatsSchemaMatchesPayload(t *testing.T) {
	schema := userStatsSchema()
	if typ, _ := schema["type"].(string); typ != "object" {
		t.Fatalf("schema type = %q, want object", typ)
	}
	compareObjectSchema(t, schema, reflect.TypeOf(statsengine.Payload{}))
}

// compareObjectSchema checks one object level, then recurses into its property
// types. A map-typed Go field (the ratings distribution) is checked against the
// schema's fixed bucket keys instead of a struct.
func compareObjectSchema(t *testing.T, schema map[string]interface{}, typ reflect.Type) {
	t.Helper()

	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s: schema has no properties object", typ)
	}

	fields := make(map[string]reflect.Type, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		fields[name] = field.Type
		if _, ok := props[name]; !ok {
			t.Errorf("%s.%s is serialised but not in the schema", typ.Name(), name)
		}
	}
	for name := range props {
		if _, ok := fields[name]; !ok {
			t.Errorf("schema property %q has no field on %s", name, typ.Name())
		}
	}

	// Every required entry must be a real property (a required key that does
	// not exist would generate a spec no response can satisfy).
	if required, ok := schema["required"].([]string); ok {
		for _, name := range required {
			if _, ok := props[name]; !ok {
				t.Errorf("%s: %q is required but not declared", typ.Name(), name)
			}
		}
	} else {
		t.Errorf("%s: schema has no required list", typ.Name())
	}

	for name, raw := range props {
		prop, ok := raw.(map[string]interface{})
		if !ok {
			t.Errorf("%s.%s: property is not an object schema", typ.Name(), name)
			continue
		}
		fieldType, ok := fields[name]
		if !ok {
			continue
		}
		switch propType, _ := prop["type"].(string); propType {
		case "object":
			compareDistributionOrStruct(t, typ.Name()+"."+name, prop, fieldType)
		case "integer":
			if fieldType.Kind() != reflect.Int64 {
				t.Errorf("%s.%s: schema says integer, Go field is %s", typ.Name(), name, fieldType.Kind())
			}
		default:
			t.Errorf("%s.%s: unexpected schema type %q", typ.Name(), name, propType)
		}
	}
}

// compareDistributionOrStruct recurses into a nested struct, or — for the
// distribution's map — asserts the ten 1..10 buckets and their float type.
func compareDistributionOrStruct(t *testing.T, path string, prop map[string]interface{}, fieldType reflect.Type) {
	t.Helper()
	if fieldType.Kind() == reflect.Map {
		buckets, ok := prop["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: distribution has no properties", path)
		}
		if len(buckets) != statsengine.DistributionBuckets {
			t.Errorf("%s: %d buckets in the schema, want %d", path, len(buckets), statsengine.DistributionBuckets)
		}
		for i := 1; i <= statsengine.DistributionBuckets; i++ {
			bucket, ok := buckets[strconv.Itoa(i)].(map[string]interface{})
			if !ok {
				t.Errorf("%s: bucket %d is missing", path, i)
				continue
			}
			if bucket["type"] != "number" || bucket["format"] != "float" {
				t.Errorf("%s: bucket %d = %v, want number/float", path, i, bucket)
			}
		}
		return
	}
	if fieldType.Kind() != reflect.Struct {
		t.Fatalf("%s: schema says object, Go field is %s", path, fieldType.Kind())
	}
	compareObjectSchema(t, prop, fieldType)
}

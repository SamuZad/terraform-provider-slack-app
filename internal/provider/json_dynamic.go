package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var errUnknownValue = errors.New("value is not yet known")

// attrToGo converts a fully known attr.Value into a JSON-marshalable Go
// value. Returns errUnknownValue when any leaf is unknown.
func attrToGo(value attr.Value) (interface{}, error) {
	if value == nil || value.IsNull() {
		return nil, nil
	}
	if value.IsUnknown() {
		return nil, errUnknownValue
	}

	switch v := value.(type) {
	case types.String:
		return v.ValueString(), nil
	case types.Bool:
		return v.ValueBool(), nil
	case types.Number:
		f := v.ValueBigFloat()
		if f.IsInt() {
			i, _ := f.Int64()
			return i, nil
		}
		fl, _ := f.Float64()
		return fl, nil
	case types.Object:
		result := map[string]interface{}{}
		for name, av := range v.Attributes() {
			converted, err := attrToGo(av)
			if err != nil {
				return nil, err
			}
			result[name] = converted
		}
		return result, nil
	case types.Map:
		result := map[string]interface{}{}
		for name, av := range v.Elements() {
			converted, err := attrToGo(av)
			if err != nil {
				return nil, err
			}
			result[name] = converted
		}
		return result, nil
	case types.Tuple:
		return elementsToGo(v.Elements())
	case types.List:
		return elementsToGo(v.Elements())
	case types.Set:
		return elementsToGo(v.Elements())
	default:
		return nil, fmt.Errorf("unsupported manifest value type %T", value)
	}
}

func elementsToGo(elements []attr.Value) ([]interface{}, error) {
	result := make([]interface{}, 0, len(elements))
	for _, element := range elements {
		converted, err := attrToGo(element)
		if err != nil {
			return nil, err
		}
		result = append(result, converted)
	}
	return result, nil
}

// goToAttr converts a decoded JSON value into an attr.Value, using objects
// and tuples so arbitrary manifest shapes round-trip.
func goToAttr(ctx context.Context, value interface{}) attr.Value {
	switch v := value.(type) {
	case nil:
		return types.StringNull()
	case string:
		return types.StringValue(v)
	case bool:
		return types.BoolValue(v)
	case float64:
		return types.NumberValue(big.NewFloat(v))
	case map[string]interface{}:
		attrTypes := map[string]attr.Type{}
		attrValues := map[string]attr.Value{}
		for name, element := range v {
			converted := goToAttr(ctx, element)
			attrValues[name] = converted
			attrTypes[name] = converted.Type(ctx)
		}
		return types.ObjectValueMust(attrTypes, attrValues)
	case []interface{}:
		elementTypes := make([]attr.Type, 0, len(v))
		elements := make([]attr.Value, 0, len(v))
		for _, element := range v {
			converted := goToAttr(ctx, element)
			elements = append(elements, converted)
			elementTypes = append(elementTypes, converted.Type(ctx))
		}
		return types.TupleValueMust(elementTypes, elements)
	default:
		return types.StringValue(fmt.Sprintf("%v", v))
	}
}

// dynamicManifestJSON renders a fully known object-form manifest value as a
// JSON string. Returns errUnknownValue if any part is unknown.
func dynamicManifestJSON(d types.Dynamic) (string, error) {
	if d.IsNull() || d.IsUnderlyingValueNull() {
		return "", errors.New("manifest is null")
	}
	if d.IsUnknown() || d.IsUnderlyingValueUnknown() {
		return "", errUnknownValue
	}
	converted, err := attrToGo(d.UnderlyingValue())
	if err != nil {
		return "", err
	}
	marshaled, err := json.Marshal(converted)
	if err != nil {
		return "", err
	}
	return string(marshaled), nil
}

// attrField returns the named field of an object- or map-typed value.
func attrField(value attr.Value, name string) (attr.Value, bool) {
	switch v := value.(type) {
	case types.Object:
		field, ok := v.Attributes()[name]
		return field, ok
	case types.Map:
		field, ok := v.Elements()[name]
		return field, ok
	default:
		return nil, false
	}
}

// stringsFromValue extracts a fully known list of strings from a
// tuple/list/set value; known is false when the collection or any element is
// unknown.
func stringsFromValue(value attr.Value) (result []string, known bool) {
	if value == nil || value.IsNull() {
		return nil, true
	}
	if value.IsUnknown() {
		return nil, false
	}
	var elements []attr.Value
	switch v := value.(type) {
	case types.Tuple:
		elements = v.Elements()
	case types.List:
		elements = v.Elements()
	case types.Set:
		elements = v.Elements()
	default:
		return nil, false
	}
	for _, element := range elements {
		s, ok := element.(types.String)
		if !ok || s.IsUnknown() {
			return nil, false
		}
		if !s.IsNull() {
			result = append(result, s.ValueString())
		}
	}
	return result, true
}

// dynamicScopes extracts the OAuth scopes from a planned manifest value.
// The value may be partially unknown: as long as the oauth_config.scopes
// subtree is known, the scopes are known — an unknown leaf elsewhere (e.g. a
// computed description) does not make the scopes unknown. known is false only
// when the scopes themselves cannot be determined yet.
func dynamicScopes(d types.Dynamic) (scopes oauthScopes, known bool) {
	if d.IsNull() || d.IsUnknown() || d.IsUnderlyingValueNull() || d.IsUnderlyingValueUnknown() {
		return scopes, false
	}
	underlying := d.UnderlyingValue()

	oauthConfig, ok := attrField(underlying, "oauth_config")
	if !ok || oauthConfig == nil || oauthConfig.IsNull() {
		return scopes, true // no scopes declared
	}
	if oauthConfig.IsUnknown() {
		return scopes, false
	}
	scopesValue, ok := attrField(oauthConfig, "scopes")
	if !ok || scopesValue == nil || scopesValue.IsNull() {
		return scopes, true
	}
	if scopesValue.IsUnknown() {
		return scopes, false
	}

	bot, botKnown := stringsFromValue(fieldOrNil(scopesValue, "bot"))
	user, userKnown := stringsFromValue(fieldOrNil(scopesValue, "user"))
	if !botKnown || !userKnown {
		return scopes, false
	}
	return oauthScopes{Bot: bot, User: user}, true
}

func fieldOrNil(value attr.Value, name string) attr.Value {
	field, ok := attrField(value, name)
	if !ok {
		return nil
	}
	return field
}

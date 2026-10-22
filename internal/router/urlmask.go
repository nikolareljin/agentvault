package router

import (
	"errors"
	"net/url"
	"strings"
)

// MaskURL masks the credentials in a URL before it is shown. The cmd
// package sets it to its redaction helper; the default hides any value
// with an "@" or a query, which covers userinfo and key parameters.
var MaskURL = func(raw string) string {
	if strings.ContainsAny(raw, "@?#") {
		return "<secret>"
	}
	return raw
}

// maskURLError masks, through MaskURL, the URL that a *url.Error in err
// quotes, and returns err.
func maskURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		ue.URL = MaskURL(ue.URL)
	}
	return err
}

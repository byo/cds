package main

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/go-gorp/gorp"
	"github.com/prometheus/common/expfmt"

	"github.com/ovh/cds/engine/api/businesscontext"
	"github.com/ovh/cds/engine/api/metrics"
	"github.com/ovh/cds/sdk"
)

func getMetrics(w http.ResponseWriter, req *http.Request, db *gorp.DbMap, c *businesscontext.Ctx) error {
	mfs, err := metrics.GetGatherer().Gather()
	if err != nil {
		return sdk.WrapError(err, "An error has occurred during metrics gathering")
	}
	contentType := expfmt.Negotiate(req.Header)
	writer := &bytes.Buffer{}
	enc := expfmt.NewEncoder(writer, contentType)
	for _, mf := range mfs {
		if err := enc.Encode(mf); err != nil {
			return sdk.WrapError(err, "metrics> An error has occurred during metrics encoding")
		}
	}
	header := w.Header()
	header.Set("Content-Type", string(contentType))
	header.Set("Content-Length", fmt.Sprint(writer.Len()))
	w.Write(writer.Bytes())
	return nil
}

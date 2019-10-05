/*
 * MinIO Cloud Storage, (C) 2018 MinIO, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package iampolicy

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"

	xnet "github.com/minio/minio/pkg/net"
)

// OpaArgs opa general purpose policy engine configuration.
type OpaArgs struct {
	URL         *xnet.URL             `json:"url"`
	AuthToken   string                `json:"authToken"`
	Transport   http.RoundTripper     `json:"-"`
	CloseRespFn func(r io.ReadCloser) `json:"-"`
}

// Validate - validate opa configuration params.
func (a *OpaArgs) Validate() error {
	req, err := http.NewRequest("POST", a.URL.String(), bytes.NewReader([]byte("")))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	if a.AuthToken != "" {
		req.Header.Set("Authorization", a.AuthToken)
	}

	client := &http.Client{Transport: a.Transport}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer a.CloseRespFn(resp.Body)

	return nil
}

// UnmarshalJSON - decodes JSON data.
func (a *OpaArgs) UnmarshalJSON(data []byte) error {
	// subtype to avoid recursive call to UnmarshalJSON()
	type subOpaArgs OpaArgs
	var so subOpaArgs

	if err := json.Unmarshal(data, &so); err != nil {
		return err
	}

	oa := OpaArgs(so)
	if oa.URL == nil || oa.URL.String() == "" {
		*a = oa
		return nil
	}

	*a = oa
	return nil
}

// Opa - implements opa policy agent calls.
type Opa struct {
	args   OpaArgs
	client *http.Client
}

// NewOpa - initializes opa policy engine connector.
func NewOpa(args OpaArgs) *Opa {
	// No opa args.
	if args.URL == nil && args.AuthToken == "" {
		return nil
	}
	return &Opa{
		args:   args,
		client: &http.Client{Transport: args.Transport},
	}
}

// IsAllowed - checks given policy args is allowed to continue the REST API.
func (o *Opa) IsAllowed(args Args) (bool, error) {
	if o == nil {
		return false, nil
	}

	// OPA input
	body := make(map[string]interface{})
	body["input"] = args

	inputBytes, err := json.Marshal(body)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequest("POST", o.args.URL.String(), bytes.NewReader(inputBytes))
	if err != nil {
		return false, err
	}

	req.Header.Set("Content-Type", "application/json")
	if o.args.AuthToken != "" {
		req.Header.Set("Authorization", o.args.AuthToken)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return false, err
	}
	defer o.args.CloseRespFn(resp.Body)

	// Read the body to be saved later.
	opaRespBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	// Handle large OPA responses when OPA URL is of
	// form http://localhost:8181/v1/data/httpapi/authz
	type opaResultAllow struct {
		Result struct {
			Allow bool `json:"allow"`
		} `json:"result"`
	}

	// Handle simpler OPA responses when OPA URL is of
	// form http://localhost:8181/v1/data/httpapi/authz/allow
	type opaResult struct {
		Result bool `json:"result"`
	}

	respBody := bytes.NewReader(opaRespBytes)

	var result opaResult
	if err = json.NewDecoder(respBody).Decode(&result); err != nil {
		respBody.Seek(0, 0)
		var resultAllow opaResultAllow
		if err = json.NewDecoder(respBody).Decode(&resultAllow); err != nil {
			return false, err
		}
		return resultAllow.Result.Allow, nil
	}
	return result.Result, nil
}

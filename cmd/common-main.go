/*
 * MinIO Cloud Storage, (C) 2017, 2018 MinIO, Inc.
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

package cmd

import (
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	dns2 "github.com/miekg/dns"
	"github.com/minio/cli"
	"github.com/minio/minio-go/v6/pkg/set"
	"github.com/minio/minio/cmd/config"
	"github.com/minio/minio/cmd/logger"
	"github.com/minio/minio/cmd/logger/target/http"
	"github.com/minio/minio/pkg/auth"
	"github.com/minio/minio/pkg/dns"
	"github.com/minio/minio/pkg/env"
	xnet "github.com/minio/minio/pkg/net"
)

func verifyObjectLayerFeatures(name string, objAPI ObjectLayer) {
	if (globalAutoEncryption || GlobalKMS != nil) && !objAPI.IsEncryptionSupported() {
		logger.Fatal(errInvalidArgument,
			"Encryption support is requested but '%s' does not support encryption", name)
	}

	if strings.HasPrefix(name, "gateway") {
		if GlobalGatewaySSE.IsSet() && GlobalKMS == nil {
			uiErr := config.ErrInvalidGWSSEEnvValue(nil).Msg("MINIO_GATEWAY_SSE set but KMS is not configured")
			logger.Fatal(uiErr, "Unable to start gateway with SSE")
		}
	}

	if globalIsCompressionEnabled && !objAPI.IsCompressionSupported() {
		logger.Fatal(errInvalidArgument,
			"Compression support is requested but '%s' does not support compression", name)
	}
}

// Check for updates and print a notification message
func checkUpdate(mode string) {
	// Its OK to ignore any errors during doUpdate() here.
	if updateMsg, _, currentReleaseTime, latestReleaseTime, err := getUpdateInfo(2*time.Second, mode); err == nil {
		if updateMsg == "" {
			return
		}
		if globalInplaceUpdateDisabled {
			logStartupMessage(updateMsg)
		} else {
			logStartupMessage(prepareUpdateMessage("Run `mc admin update`", latestReleaseTime.Sub(currentReleaseTime)))
		}
	}
}

// Load logger targets based on user's configuration
func loadLoggers() {
	loggerUserAgent := getUserAgent(getMinioMode())

	auditEndpoint, ok := env.Lookup("MINIO_AUDIT_LOGGER_HTTP_ENDPOINT")
	if ok {
		// Enable audit HTTP logging through ENV.
		logger.AddAuditTarget(http.New(auditEndpoint, loggerUserAgent, NewCustomHTTPTransport()))
	}

	loggerEndpoint, ok := env.Lookup("MINIO_LOGGER_HTTP_ENDPOINT")
	if ok {
		// Enable HTTP logging through ENV.
		logger.AddTarget(http.New(loggerEndpoint, loggerUserAgent, NewCustomHTTPTransport()))
	} else {
		for _, l := range globalServerConfig.Logger.HTTP {
			if l.Enabled {
				// Enable http logging
				logger.AddTarget(http.New(l.Endpoint, loggerUserAgent, NewCustomHTTPTransport()))
			}
		}
	}

	if globalServerConfig.Logger.Console.Enabled {
		// Enable console logging
		logger.AddTarget(globalConsoleSys.Console())
	}

}

func newConfigDirFromCtx(ctx *cli.Context, option string, getDefaultDir func() string) (*ConfigDir, bool) {
	var dir string
	var dirSet bool

	switch {
	case ctx.IsSet(option):
		dir = ctx.String(option)
		dirSet = true
	case ctx.GlobalIsSet(option):
		dir = ctx.GlobalString(option)
		dirSet = true
		// cli package does not expose parent's option option.  Below code is workaround.
		if dir == "" || dir == getDefaultDir() {
			dirSet = false // Unset to false since GlobalIsSet() true is a false positive.
			if ctx.Parent().GlobalIsSet(option) {
				dir = ctx.Parent().GlobalString(option)
				dirSet = true
			}
		}
	default:
		// Neither local nor global option is provided.  In this case, try to use
		// default directory.
		dir = getDefaultDir()
		if dir == "" {
			logger.FatalIf(errInvalidArgument, "%s option must be provided", option)
		}
	}

	if dir == "" {
		logger.FatalIf(errors.New("empty directory"), "%s directory cannot be empty", option)
	}

	// Disallow relative paths, figure out absolute paths.
	dirAbs, err := filepath.Abs(dir)
	logger.FatalIf(err, "Unable to fetch absolute path for %s=%s", option, dir)

	logger.FatalIf(mkdirAllIgnorePerm(dirAbs), "Unable to create directory specified %s=%s", option, dir)

	return &ConfigDir{path: dirAbs}, dirSet
}

func handleCommonCmdArgs(ctx *cli.Context) {

	// Get "json" flag from command line argument and
	// enable json and quite modes if json flag is turned on.
	globalCLIContext.JSON = ctx.IsSet("json") || ctx.GlobalIsSet("json")
	if globalCLIContext.JSON {
		logger.EnableJSON()
	}

	// Get quiet flag from command line argument.
	globalCLIContext.Quiet = ctx.IsSet("quiet") || ctx.GlobalIsSet("quiet")
	if globalCLIContext.Quiet {
		logger.EnableQuiet()
	}

	// Get anonymous flag from command line argument.
	globalCLIContext.Anonymous = ctx.IsSet("anonymous") || ctx.GlobalIsSet("anonymous")
	if globalCLIContext.Anonymous {
		logger.EnableAnonymous()
	}

	// Fetch address option
	globalCLIContext.Addr = ctx.GlobalString("address")
	if globalCLIContext.Addr == "" || globalCLIContext.Addr == ":"+globalMinioDefaultPort {
		globalCLIContext.Addr = ctx.String("address")
	}

	// Set all config, certs and CAs directories.
	var configSet, certsSet bool
	globalConfigDir, configSet = newConfigDirFromCtx(ctx, "config-dir", defaultConfigDir.Get)
	globalCertsDir, certsSet = newConfigDirFromCtx(ctx, "certs-dir", defaultCertsDir.Get)

	// Remove this code when we deprecate and remove config-dir.
	// This code is to make sure we inherit from the config-dir
	// option if certs-dir is not provided.
	if !certsSet && configSet {
		globalCertsDir = &ConfigDir{path: filepath.Join(globalConfigDir.Get(), certsDir)}
	}

	globalCertsCADir = &ConfigDir{path: filepath.Join(globalCertsDir.Get(), certsCADir)}

	logger.FatalIf(mkdirAllIgnorePerm(globalCertsCADir.Get()), "Unable to create certs CA directory at %s", globalCertsCADir.Get())

	// Check "compat" flag from command line argument.
	globalCLIContext.StrictS3Compat = ctx.IsSet("compat") || ctx.GlobalIsSet("compat")
}

func handleCommonEnvVars() {
	// Start profiler if env is set.
	if profiler := env.Get("_MINIO_PROFILER", ""); profiler != "" {
		var err error
		globalProfiler, err = startProfiler(profiler, "")
		logger.FatalIf(err, "Unable to setup a profiler")
	}

	accessKey := env.Get("MINIO_ACCESS_KEY", "")
	secretKey := env.Get("MINIO_SECRET_KEY", "")
	if accessKey != "" && secretKey != "" {
		cred, err := auth.CreateCredentials(accessKey, secretKey)
		if err != nil {
			logger.Fatal(config.ErrInvalidCredentials(err), "Unable to validate credentials inherited from the shell environment")
		}
		cred.Expiration = timeSentinel

		// credential Envs are set globally.
		globalIsEnvCreds = true
		globalActiveCred = cred
	}

	if browser := env.Get("MINIO_BROWSER", "on"); browser != "" {
		browserFlag, err := ParseBoolFlag(browser)
		if err != nil {
			logger.Fatal(config.ErrInvalidBrowserValue(nil).Msg("Unknown value `%s`", browser), "Invalid MINIO_BROWSER value in environment variable")
		}

		// browser Envs are set globally, this does not represent
		// if browser is turned off or on.
		globalIsEnvBrowser = true
		globalIsBrowserEnabled = bool(browserFlag)
	}

	etcdEndpointsEnv, ok := env.Lookup("MINIO_ETCD_ENDPOINTS")
	if ok {
		etcdEndpoints := strings.Split(etcdEndpointsEnv, ",")

		var etcdSecure bool
		for _, endpoint := range etcdEndpoints {
			u, err := xnet.ParseURL(endpoint)
			if err != nil {
				logger.FatalIf(err, "Unable to initialize etcd with %s", etcdEndpoints)
			}
			// If one of the endpoint is https, we will use https directly.
			etcdSecure = etcdSecure || u.Scheme == "https"
		}

		var err error
		if etcdSecure {
			// This is only to support client side certificate authentication
			// https://coreos.com/etcd/docs/latest/op-guide/security.html
			etcdClientCertFile, ok1 := env.Lookup("MINIO_ETCD_CLIENT_CERT")
			etcdClientCertKey, ok2 := env.Lookup("MINIO_ETCD_CLIENT_CERT_KEY")
			var getClientCertificate func(*tls.CertificateRequestInfo) (*tls.Certificate, error)
			if ok1 && ok2 {
				getClientCertificate = func(unused *tls.CertificateRequestInfo) (*tls.Certificate, error) {
					cert, terr := tls.LoadX509KeyPair(etcdClientCertFile, etcdClientCertKey)
					return &cert, terr
				}
			}

			globalEtcdClient, err = etcd.New(etcd.Config{
				Endpoints:         etcdEndpoints,
				DialTimeout:       defaultDialTimeout,
				DialKeepAliveTime: defaultDialKeepAlive,
				TLS: &tls.Config{
					RootCAs:              globalRootCAs,
					GetClientCertificate: getClientCertificate,
				},
			})
		} else {
			globalEtcdClient, err = etcd.New(etcd.Config{
				Endpoints:         etcdEndpoints,
				DialTimeout:       defaultDialTimeout,
				DialKeepAliveTime: defaultDialKeepAlive,
			})
		}
		logger.FatalIf(err, "Unable to initialize etcd with %s", etcdEndpoints)
	}

	v, ok := env.Lookup("MINIO_DOMAIN")
	if ok {
		for _, domainName := range strings.Split(v, ",") {
			if _, ok = dns2.IsDomainName(domainName); !ok {
				logger.Fatal(config.ErrInvalidDomainValue(nil).Msg("Unknown value `%s`", domainName),
					"Invalid MINIO_DOMAIN value in environment variable")
			}
			globalDomainNames = append(globalDomainNames, domainName)
		}
	}

	minioEndpointsEnv, ok := env.Lookup("MINIO_PUBLIC_IPS")
	if ok {
		minioEndpoints := strings.Split(minioEndpointsEnv, ",")
		var domainIPs = set.NewStringSet()
		for _, endpoint := range minioEndpoints {
			if net.ParseIP(endpoint) == nil {
				// Checking if the IP is a DNS entry.
				addrs, err := net.LookupHost(endpoint)
				if err != nil {
					logger.FatalIf(err, "Unable to initialize MinIO server with [%s] invalid entry found in MINIO_PUBLIC_IPS", endpoint)
				}
				for _, addr := range addrs {
					domainIPs.Add(addr)
				}
				continue
			}
			domainIPs.Add(endpoint)
		}
		updateDomainIPs(domainIPs)
	} else {
		// Add found interfaces IP address to global domain IPS,
		// loopback addresses will be naturally dropped.
		updateDomainIPs(localIP4)
	}

	if len(globalDomainNames) != 0 && !globalDomainIPs.IsEmpty() && globalEtcdClient != nil {
		var err error
		globalDNSConfig, err = dns.NewCoreDNS(globalDomainNames, globalDomainIPs, globalMinioPort, globalEtcdClient)
		logger.FatalIf(err, "Unable to initialize DNS config for %s.", globalDomainNames)
	}

	// In place update is true by default if the MINIO_UPDATE is not set
	// or is not set to 'off', if MINIO_UPDATE is set to 'off' then
	// in-place update is off.
	globalInplaceUpdateDisabled = strings.EqualFold(env.Get("MINIO_UPDATE", "off"), "off")

	// Validate and store the storage class env variables only for XL/Dist XL setups
	if globalIsXL {
		var err error

		// Check for environment variables and parse into storageClass struct
		if ssc := os.Getenv(standardStorageClassEnv); ssc != "" {
			globalStandardStorageClass, err = parseStorageClass(ssc)
			logger.FatalIf(err, "Invalid value set in environment variable %s", standardStorageClassEnv)
		}

		if rrsc := os.Getenv(reducedRedundancyStorageClassEnv); rrsc != "" {
			globalRRStorageClass, err = parseStorageClass(rrsc)
			logger.FatalIf(err, "Invalid value set in environment variable %s", reducedRedundancyStorageClassEnv)
		}

		// Validation is done after parsing both the storage classes. This is needed because we need one
		// storage class value to deduce the correct value of the other storage class.
		if globalRRStorageClass.Scheme != "" {
			err = validateParity(globalStandardStorageClass.Parity, globalRRStorageClass.Parity)
			logger.FatalIf(err, "Invalid value set in environment variable %s", reducedRedundancyStorageClassEnv)
			globalIsStorageClass = true
		}

		if globalStandardStorageClass.Scheme != "" {
			err = validateParity(globalStandardStorageClass.Parity, globalRRStorageClass.Parity)
			logger.FatalIf(err, "Invalid value set in environment variable %s", standardStorageClassEnv)
			globalIsStorageClass = true
		}
	}

	// Get WORM environment variable.
	if worm := env.Get("MINIO_WORM", "off"); worm != "" {
		wormFlag, err := ParseBoolFlag(worm)
		if err != nil {
			logger.Fatal(config.ErrInvalidWormValue(nil).Msg("Unknown value `%s`", worm), "Invalid MINIO_WORM value in environment variable")
		}

		// worm Envs are set globally, this does not represent
		// if worm is turned off or on.
		globalIsEnvWORM = true
		globalWORMEnabled = bool(wormFlag)
	}

}

func logStartupMessage(msg string, data ...interface{}) {
	if globalConsoleSys != nil {
		globalConsoleSys.Send(msg)
	}
	logger.StartupMessage(msg, data...)
}

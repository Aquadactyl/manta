package cmd

import (
	"context"
	"errors"
	"fmt"
	log2 "log"
	"net"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // only served on localhost with --pprof; nothing else uses http.DefaultServeMux
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/apex/log"
	"github.com/apex/log/handlers/multi"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/gammazero/workerpool"
	"github.com/mitchellh/colorstring"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/internal/cron"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/network"
	"github.com/pterodactyl/wings/loggers/cli"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/sftp"
	"github.com/pterodactyl/wings/system"
)

var (
	configPath            = config.DefaultLocation
	debug                 = false
	exitCommand           = os.Exit
	fatalfCommand         = log2.Fatalf
	getNoFileLimit        = syscall.Getrlimit
	filepathAbs           = filepath.Abs
	shutdownHTTP          = (*http.Server).Shutdown
	configureTimezone     = config.ConfigureTimezone
	configureDirectories  = config.ConfigureDirectories
	ensurePterodactylUser = config.EnsurePterodactylUser
	configurePasswd       = config.ConfigurePasswd
	enableLogRotation     = config.EnableLogRotation
	initializeDatabase    = database.Initialize
	newServerManager      = server.NewManager
	configureDocker       = environment.ConfigureDocker
	writeRootConfig       = config.WriteToDisk
	rootPowerAction       = func(s *server.Server, action server.PowerAction) error { return s.HandlePowerAction(action) }
	startRootCron         = func(ctx context.Context, manager *server.Manager) error {
		s, err := cron.Scheduler(ctx, manager)
		if err != nil {
			return err
		}
		log.WithField("subsystem", "cron").Info("starting cron processes")
		s.StartAsync()
		return nil
	}
	serveRootSFTP = func(manager *server.Manager, maxConnections int) error {
		srv := sftp.New(manager)
		srv.MaxConnections = maxConnections
		return srv.Run()
	}
	resetRootServerStates = func(ctx context.Context, client remote.Client) error {
		return client.ResetServersState(ctx)
	}
	serveHTTPCommand = func(s *http.Server, listener net.Listener) error { return s.Serve(listener) }
	serveTLSCommand  = func(s *http.Server, listener net.Listener, certFile, keyFile string) error {
		return s.ServeTLS(listener, certFile, keyFile)
	}
	serveAutoTLSHTTPCommand = func(handler http.Handler) {
		go func() {
			if err := serveAutoTLSHTTPListen(router.NewServer(":http", handler, nil)); err != nil {
				log.WithError(err).Error("failed to serve autocert http server")
			}
		}()
	}
	serveAutoTLSHTTPListen = func(s *http.Server) error { return s.ListenAndServe() }
	rootFatal              = func(fields log.Fields, message string) {
		entry := log.WithFields(fields)
		fatal := &log.Entry{
			Logger:    entry.Logger,
			Fields:    fields,
			Level:     log.FatalLevel,
			Timestamp: time.Now(),
			Message:   message,
		}
		_ = entry.Logger.Handler.HandleLog(fatal)
		exitCommand(1)
	}
)

var rootCommand = &cobra.Command{
	Use:   "manta",
	Short: "Runs the API server allowing programmatic control of game servers for Aquadactyl Panel.",
	PreRun: func(cmd *cobra.Command, args []string) {
		initConfig()
		initLogging()
		if tls, _ := cmd.Flags().GetBool("auto-tls"); tls {
			if host, _ := cmd.Flags().GetString("tls-hostname"); host == "" {
				fmt.Println("A TLS hostname must be provided when running manta with automatic TLS, e.g.:\n\n    ./manta --auto-tls --tls-hostname my.example.com")
				exitCommand(1)
			}
		}
	},
	Run: rootCmdRun,
}

var versionCommand = &cobra.Command{
	Use:   "version",
	Short: "Prints the current executable version and exits.",
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Printf("manta %s\nCopyright © 2018 - %d Dane Everitt & Contributors\n", system.Version, time.Now().Year())
	},
}

func Execute() {
	if err := rootCommand.Execute(); err != nil {
		fatalfCommand("failed to execute command: %s", err)
	}
}

func init() {
	rootCommand.PersistentFlags().StringVar(&configPath, "config", config.DefaultLocation, "set the location for the configuration file")
	rootCommand.PersistentFlags().BoolVar(&debug, "debug", false, "pass in order to run manta in debug mode")

	// Flags specifically used when running the API.
	rootCommand.Flags().Bool("pprof", false, "if the pprof profiler should be enabled. The profiler will bind to localhost:6060 by default")
	rootCommand.Flags().Int("pprof-block-rate", 0, "enables block profile support, may have performance impacts")
	rootCommand.Flags().Int("pprof-port", 6060, "If provided with --pprof, the port it will run on")
	rootCommand.Flags().Bool("auto-tls", false, "pass in order to have manta generate and manage its own SSL certificates using Let's Encrypt")
	rootCommand.Flags().String("tls-hostname", "", "required with --auto-tls, the FQDN for the generated SSL certificate")
	rootCommand.Flags().Bool("ignore-certificate-errors", false, "ignore certificate verification errors when executing API calls (deprecated)")
	_ = rootCommand.Flags().MarkDeprecated("ignore-certificate-errors", "insecure certificate error bypass is no longer supported; configure a valid TLS certificate chain instead")

	rootCommand.AddCommand(versionCommand)
	rootCommand.AddCommand(configureCmd)
	rootCommand.AddCommand(newDiagnosticsCommand())
}

func rootCmdRun(cmd *cobra.Command, _ []string) {
	printLogo()
	log.Debug("running in debug mode")
	log.WithField("config_file", configPath).Info("loading configuration from file")

	if ok, _ := cmd.Flags().GetBool("ignore-certificate-errors"); ok {
		rootFatal(nil, "the --ignore-certificate-errors flag is insecure and is no longer supported; configure a valid TLS certificate chain and hostname trust instead")
		return
	}

	if err := configureTimezone(); err != nil {
		rootFatal(log.Fields{"error": err}, "failed to detect system timezone or use supplied configuration value")
		return
	}
	log.WithField("timezone", config.Get().System.Timezone).Info("configured manta with system timezone")
	if err := configureDirectories(); err != nil {
		rootFatal(log.Fields{"error": err}, "failed to configure system directories for manta")
		return
	}
	if err := ensurePterodactylUser(); err != nil {
		rootFatal(log.Fields{"error": err}, "failed to create manta system user")
		return
	}
	if err := configurePasswd(); err != nil {
		rootFatal(log.Fields{"error": err}, "failed to configure container passwd file")
		return
	}
	log.WithFields(log.Fields{
		"username": config.Get().System.Username,
		"uid":      config.Get().System.User.Uid,
		"gid":      config.Get().System.User.Gid,
	}).Info("configured system user successfully")
	if err := enableLogRotation(); err != nil {
		rootFatal(log.Fields{"error": err}, "failed to configure log rotation on the system")
		return
	}

	t := config.Get().Token
	pclient := remote.New(
		config.Get().PanelLocation,
		remote.WithCredentials(t.ID, t.Token),
		remote.WithHttpClient(&http.Client{
			Timeout: time.Second * time.Duration(config.Get().RemoteQuery.Timeout),
		}),
	)

	if err := initializeDatabase(); err != nil {
		rootFatal(log.Fields{"error": err}, "failed to initialize database")
		return
	}

	manager, err := newServerManager(cmd.Context(), pclient)
	if err != nil {
		rootFatal(log.Fields{"error": err}, "failed to load server configurations")
		return
	}

	if err := configureDocker(cmd.Context()); err != nil {
		rootFatal(log.Fields{"error": err}, "failed to configure docker environment")
		return
	}
	startMountCheck(cmd.Context())

	writeConfigWithLogging()

	// Just for some nice log output.
	for _, s := range manager.All() {
		log.WithField("server", s.ID()).Info("finished loading configuration for server")
	}

	states := readRootServerStates(manager)

	ticker := time.NewTicker(time.Minute)
	// Every minute, write the current server states to the disk to allow for a more
	// seamless hard-reboot process in which wings will re-sync server states based
	// on its last tracked state.
	go func() {
		persistRootStatesUntilDone(cmd.Context(), ticker, manager.PersistStates)
	}()

	// Create a new workerpool that limits us to 4 servers being bootstrapped at a time
	// on Wings. This allows us to ensure the environment exists, write configurations,
	// and reboot processes without causing a slow-down due to sequential booting.
	pool := workerpool.New(4)
	for _, serv := range manager.All() {
		s := serv

		// For each server ensure the minimal environment is configured for the server.
		if err := s.CreateEnvironment(); err != nil {
			s.Log().Error("could create base environment for server...")
			continue
		}

		pool.Submit(func() {
			s.Log().Info("configuring server environment and restoring to previous state")
			var st string
			if state, exists := states[s.ID()]; exists {
				st = state
			}

			// Use a timed context here to avoid booting issues where Docker hangs for a
			// specific container that would cause Wings to be un-bootable until the entire
			// machine is rebooted. It is much better for us to just have a single failed
			// server instance than an entire offline node.
			//
			// @see https://github.com/pterodactyl/panel/issues/2475
			// @see https://github.com/pterodactyl/panel/issues/3358
			ctx, cancel := context.WithTimeout(cmd.Context(), time.Second*30)
			defer cancel()

			r, err := s.Environment.IsRunning(ctx)
			// We ignore missing containers because we don't want to actually block booting of wings at this
			// point. If we didn't do this, and you pruned all the images and then started wings you could
			// end up waiting a long period of time for all the images to be re-pulled on Wings boot rather
			// than when the server itself is started.
			if err != nil && !cerrdefs.IsNotFound(err) {
				s.Log().WithField("error", err).Error("error checking server environment status")
			}

			// Check if the server was previously running. If so, attempt to start the server now so that Wings
			// can pick up where it left off. If the environment does not exist at all, just create it and then allow
			// the normal flow to execute.
			//
			// This does mean that booting wings after a catastrophic machine crash and wiping out the Docker images
			// as a result will result in a slow boot.
			if !r && (st == environment.ProcessRunningState || st == environment.ProcessStartingState) {
				if err := rootPowerAction(s, server.PowerActionStart); err != nil {
					s.Log().WithField("error", err).Warn("failed to return server to running state")
				}
			} else if r || (!r && s.IsRunning()) {
				// If the server is currently running on Docker, mark the process as being in that state.
				// We never want to stop an instance that is currently running external from Wings since
				// that is a good way of keeping things running even if Wings gets in a very corrupted state.
				//
				// This will also validate that a server process is running if the last tracked state we have
				// is that it was running, but we see that the container process is not currently running.
				s.Log().Info("detected server is running, re-attaching to process...")

				s.Environment.SetState(environment.ProcessRunningState)
				if err := s.Environment.Attach(ctx); err != nil {
					s.Log().WithField("error", err).Warn("failed to attach to running server environment")
				}
			} else {
				// At this point we've determined that the server should indeed be in an offline state, so we'll
				// make a call to set that state just to ensure we don't ever accidentally end up with some invalid
				// state being tracked.
				s.Environment.SetState(environment.ProcessOfflineState)
			}

			if state := s.Environment.State(); state == environment.ProcessStartingState || state == environment.ProcessRunningState {
				s.Log().Debug("re-syncing server configuration for already running server")
				if err := s.Sync(); err != nil {
					s.Log().WithError(err).Error("failed to re-sync server configuration")
				}
			}
		})
	}

	// Wait until all the servers are ready to go before we fire up the SFTP and HTTP servers.
	pool.StopWait()
	defer func() {
		// Cancel the context on all the running servers at this point, even though the
		// program is just shutting down.
		for _, s := range manager.All() {
			s.CtxCancel()
		}
	}()

	startRootCronOrFatal(cmd.Context(), manager)

	httpConnections, sftpConnections := connectionLimits()
	log.WithFields(log.Fields{"http": httpConnections, "sftp": sftpConnections}).Info("limiting the number of open connections, raise the open file limit for Wings to allow more")

	go func() {
		// Run the SFTP server.
		runRootSFTPOrFatal(manager, sftpConnections)
	}()

	go func() {
		resetRootStatesOnPanel(cmd.Context(), pclient)
	}()

	sys := config.Get().System
	ensureRootStorageDirectories(sys)

	autotls, _ := cmd.Flags().GetBool("auto-tls")
	tlshostname, _ := cmd.Flags().GetString("tls-hostname")
	if autotls && tlshostname == "" {
		autotls = false
	}

	api := config.Get().Api
	log.WithFields(log.Fields{
		"use_ssl":      api.Ssl.Enabled,
		"use_auto_tls": autotls,
		"host_address": api.Host,
		"host_port":    api.Port,
	}).Info("configuring internal webserver")

	// Create a new HTTP server instance to handle inbound requests from the Panel
	// and external clients.
	s := router.NewServer(api.Host+":"+strconv.Itoa(api.Port), router.Configure(manager, pclient), config.DefaultTLSConfig)
	listener, err := net.Listen("tcp", s.Addr)
	reportRootHTTPListenError(err)
	listener = network.LimitListener(listener, httpConnections, "http")

	// Save the state of every server and stop serving requests when Wings is asked
	// to stop. The servers themselves keep running, and are picked up again when
	// Wings next starts.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go shutdownOnSignal(signals, manager, s)

	profile, _ := cmd.Flags().GetBool("pprof")
	if profile {
		if r, _ := cmd.Flags().GetInt("pprof-block-rate"); r > 0 {
			runtime.SetBlockProfileRate(r)
		}
		// Catch at least 1% of mutex contention issues.
		runtime.SetMutexProfileFraction(100)

		profilePort, _ := cmd.Flags().GetInt("pprof-port")
		go func() {
			srv := &http.Server{
				Addr:              fmt.Sprintf("localhost:%d", profilePort),
				ReadHeaderTimeout: time.Second * 10,
			}
			_ = srv.ListenAndServe()
		}()
	}

	serveConfiguredAPI(s, listener, api, sys, autotls, tlshostname)
}

func writeConfigWithLogging() {
	if err := writeRootConfig(config.Get()); err != nil {
		if !errors.Is(err, syscall.EROFS) {
			log.WithField("error", err).Error("failed to write configuration to disk")
		} else {
			log.WithField("error", err).Debug("failed to write configuration to disk")
		}
	}
}

func readRootServerStates(manager *server.Manager) map[string]string {
	states, err := manager.ReadStates()
	if err != nil {
		log.WithField("error", err).Error("failed to retrieve locally cached server states from disk, assuming all servers in offline state")
	}
	return states
}

func startRootCronOrFatal(ctx context.Context, manager *server.Manager) {
	if err := startRootCron(ctx, manager); err != nil {
		rootFatal(log.Fields{"error": err}, "failed to initialize cron system")
	}
}

func runRootSFTPOrFatal(manager *server.Manager, maxConnections int) {
	if err := serveRootSFTP(manager, maxConnections); err != nil {
		rootFatal(log.Fields{"error": err}, "failed to initialize the sftp server")
	}
}

func resetRootStatesOnPanel(ctx context.Context, client remote.Client) {
	log.Info("updating server states on Panel: marking installing/restoring servers as normal")
	if err := resetRootServerStates(ctx, client); err != nil {
		log.WithField("error", err).Error("failed to reset server states on Panel: some instances may be stuck in an installing/restoring state unexpectedly")
	}
}

func reportRootHTTPListenError(err error) {
	if err != nil {
		rootFatal(log.Fields{"error": err}, "failed to configure HTTP server")
	}
}

func ensureRootStorageDirectories(sys config.SystemConfiguration) {
	if err := os.MkdirAll(sys.ArchiveDirectory, 0o700); err != nil {
		log.WithField("error", err).Error("failed to create archive directory")
	}
	if err := os.MkdirAll(sys.BackupDirectory, 0o700); err != nil {
		log.WithField("error", err).Error("failed to create backup directory")
	}
}

func persistRootStatesUntilDone(ctx context.Context, ticker *time.Ticker, persist func() error) {
	for {
		select {
		case <-ticker.C:
			if err := persist(); err != nil {
				log.WithField("error", err).Warn("failed to persist server states to disk")
			}
		case <-ctx.Done():
			ticker.Stop()
			return
		}
	}
}

// serveConfiguredAPI chooses the configured TLS mode and starts the API
// listener. Keeping this separate makes the startup modes independently testable.
func serveConfiguredAPI(s *http.Server, listener net.Listener, api config.ApiConfiguration, sys config.SystemConfiguration, autotls bool, tlsHostname string) {
	if autotls {
		m := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(path.Join(sys.RootDirectory, "/.tls-cache")),
			HostPolicy: autocert.HostWhitelist(tlsHostname),
		}

		log.WithField("hostname", tlsHostname).Info("webserver is now listening with auto-TLS enabled; certificates will be automatically generated by Let's Encrypt")
		s.TLSConfig.GetCertificate = m.GetCertificate
		s.TLSConfig.NextProtos = append(s.TLSConfig.NextProtos, acme.ALPNProto)
		serveAutoTLSHTTPCommand(m.HTTPHandler(nil))
		if err := serveTLSCommand(s, listener, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			rootFatal(log.Fields{"auto_tls": true, "tls_hostname": tlsHostname, "error": err}, "failed to configure HTTP server using auto-tls")
		}
		return
	}

	if api.Ssl.Enabled {
		if err := serveTLSCommand(s, listener, api.Ssl.CertificateFile, api.Ssl.KeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			rootFatal(log.Fields{"auto_tls": false, "error": err}, "failed to configure HTTPS server")
		}
		return
	}
	s.TLSConfig = nil
	if err := serveHTTPCommand(s, listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		rootFatal(log.Fields{"error": err}, "failed to configure HTTP server")
	}
}

// shutdownOnSignal waits for a signal, then saves the state of every server and
// stops the HTTP server, which returns from Serve so that Wings can exit.
func shutdownOnSignal(signals <-chan os.Signal, manager *server.Manager, srv *http.Server) {
	sig := <-signals
	log.WithField("signal", sig.String()).Info("received signal, shutting down")
	if err := manager.PersistStates(); err != nil {
		log.WithField("error", err).Warn("failed to persist server states to disk")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := shutdownHTTP(srv, ctx); err != nil {
		log.WithField("error", err).Warn("failed to stop the HTTP server cleanly")
		_ = srv.Close()
	}
}

// connectionLimits returns the number of connections the HTTP and SFTP servers
// may each have open at once. Every connection uses a file descriptor, so the
// limits are based on the open file limit for the process, leaving room for
// everything else Wings does. Raise the open file limit to allow more.
func connectionLimits() (int, int) {
	var rl syscall.Rlimit
	if err := getNoFileLimit(syscall.RLIMIT_NOFILE, &rl); err != nil {
		return 3072, 1024
	}
	limit := rl.Cur
	if limit > 1<<20 {
		limit = 1 << 20
	}
	available := int(limit) - 1024
	if available < int(limit)/2 {
		available = int(limit) / 2
	}
	return available * 3 / 4, available / 4
}

// Reads the configuration from the disk and then sets up the global singleton
// with all the configuration values.
func initConfig() {
	if !filepath.IsAbs(configPath) {
		d, err := filepathAbs(configPath)
		if err != nil {
			fatalfCommand("cmd/root: failed to get path to config file: %s", err)
		}
		configPath = d
	}

	err := config.FromFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			exitWithConfigurationNotice()
		}
		fatalfCommand("cmd/root: error while reading configuration file: %s", err)
	}
	if debug && !config.Get().Debug {
		config.SetDebugViaFlag(debug)
	}
}

// Configures the global logger for Zap so that we can call it from any location
// in the code without having to pass around a logger instance.
func initLogging() {
	dir := config.Get().System.LogDirectory
	if err := os.MkdirAll(path.Join(dir, "/install"), 0o700); err != nil {
		fatalfCommand("cmd/root: failed to create install directory path: %s", err)
	}
	p := filepath.Join(dir, "/wings.log")
	w, err := openLogFile(p)
	if err != nil {
		fatalfCommand("cmd/root: failed to create wings log: %s", err)
	}
	log.SetLevel(log.InfoLevel)
	if config.Get().Debug {
		log.SetLevel(log.DebugLevel)
	}
	log.SetHandler(multi.New(cli.Default, cli.New(w, false)))
	log.WithField("path", p).Info("writing log files to disk")
}

// Prints the Manta logo.
func printLogo() {
	fmt.Printf(colorstring.Color(`
 __  __             _
|  \/  | __ _ _ __ | |_ __ _
| |\/| |/ _`+"`"+` | '_ \| __/ _`+"`"+` |
| |  | | (_| | | | | || (_| |
|_|  |_|\__,_|_| |_|\__\__,_|
          [blue][bold]Aquadactyl[reset] [bold]%s[reset]

Copyright © 2018 - %d Dane Everitt & Contributors

Website:  https://aquadactyl.uk
 Source:  https://github.com/Aquadactyl/manta
License:  https://github.com/Aquadactyl/manta/blob/develop/LICENSE

This software is made available under the terms of the MIT license.
The above copyright notice and this permission notice shall be included
in all copies or substantial portions of the Software.%s`), system.Version, time.Now().Year(), "\n\n")
}

func exitWithConfigurationNotice() {
	fmt.Printf(colorstring.Color(`
[_red_][white][bold]Error: Configuration File Not Found[reset]

Manta was not able to locate your configuration file, and therefore is not
able to complete its boot process. Please ensure you have copied your instance
configuration file into the default location below.

Default Location: %s

[yellow]This is not a bug with this software. Please do not make a bug report
for this issue, it will be closed.[reset]

`), config.DefaultLocation)
	exitCommand(1)
}

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	containerzclient "github.com/openconfig/containerz/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

var version = "dev"

const usageText = `cztl [connection flags] <command> [command flags]

Connection flags:
  --address host:port           SR Linux gRPC endpoint
  --ca path                     CA certificate PEM
  --cert path --key path        Optional client certificate and key
  --server-name name            TLS server name override
  --username name               SR Linux username
  --password-env variable       Environment variable containing the password
  --insecure-skip-verify        Disable server certificate verification (lab only)
  --timeout duration            RPC timeout
  --version                     Print version and exit

Commands:
  deploy         Upload a Docker-compatible image archive
  list-images    List images on the target
  start          Start a container
  create-volume  Create a volume
  remove-volume  Remove a volume
  list           List containers
  logs           Read container logs
  stop           Stop a container
  remove         Remove a container
  remove-image   Remove an image
  cleanup        Stop/remove the container and remove its image

Use "cztl <connection flags> <command> -h" for command flags.
`

type connectionConfig struct {
	address            string
	ca                 string
	cert               string
	key                string
	serverName         string
	username           string
	passwordEnv        string
	insecureSkipVerify bool
	timeout            time.Duration
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	config := connectionConfig{}
	var showVersion bool
	global := flag.NewFlagSet("cztl", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	global.StringVar(&config.address, "address", env("CONTAINERZ_ADDRESS", ""), "SR Linux gRPC endpoint")
	global.StringVar(&config.ca, "ca", env("CONTAINERZ_CA", ""), "CA certificate PEM")
	global.StringVar(&config.cert, "cert", env("CONTAINERZ_CERT", ""), "client certificate PEM")
	global.StringVar(&config.key, "key", env("CONTAINERZ_KEY", ""), "client private key PEM")
	global.StringVar(&config.serverName, "server-name", env("CONTAINERZ_SERVER_NAME", ""), "TLS server name")
	global.StringVar(&config.username, "username", env("CONTAINERZ_USERNAME", ""), "SR Linux username")
	global.StringVar(&config.passwordEnv, "password-env", "CONTAINERZ_PASSWORD", "password environment variable")
	global.BoolVar(&config.insecureSkipVerify, "insecure-skip-verify", false, "disable TLS verification (lab only)")
	global.DurationVar(&config.timeout, "timeout", 10*time.Minute, "RPC timeout")
	global.BoolVar(&showVersion, "version", false, "print version and exit")
	global.Usage = func() { fmt.Fprint(global.Output(), usageText) }

	if err := global.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		os.Exit(2)
	}
	if showVersion {
		fmt.Printf("cztl %s\n", version)
		return
	}
	args := global.Args()
	if len(args) == 0 {
		global.Usage()
		os.Exit(2)
	}
	if len(args) == 2 && (args[1] == "-h" || args[1] == "--help") {
		if err := run(context.Background(), nil, args[0], args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
			exitf("%s: %v", args[0], err)
		}
		return
	}
	if config.address == "" {
		exitf("--address or CONTAINERZ_ADDRESS is required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, config.timeout)
	defer cancel()
	ctx = withAuthentication(ctx, config)

	connection, err := dial(config)
	if err != nil {
		exitf("connect: %v", err)
	}
	defer connection.Close()
	client := containerzclient.NewClientWithConn(connection)

	if err := run(ctx, client, args[0], args[1:]); err != nil {
		exitf("%s: %v", args[0], err)
	}
}

func run(ctx context.Context, client *containerzclient.Client, command string, args []string) error {
	switch command {
	case "deploy":
		return deploy(ctx, client, args)
	case "list-images":
		return listImages(ctx, client, args)
	case "start":
		return start(ctx, client, args)
	case "create-volume":
		return createVolume(ctx, client, args)
	case "remove-volume":
		return removeVolume(ctx, client, args)
	case "list":
		return list(ctx, client, args)
	case "logs":
		return logs(ctx, client, args)
	case "stop":
		return stop(ctx, client, args)
	case "remove":
		return remove(ctx, client, args)
	case "remove-image":
		return removeImage(ctx, client, args)
	case "cleanup":
		return cleanup(ctx, client, args)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func deploy(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	file := fs.String("file", "", "image archive (required)")
	image := fs.String("image", "", "image name (required)")
	tag := fs.String("tag", "latest", "image tag")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("--file is required")
	}
	if *image == "" {
		return errors.New("--image is required")
	}
	info, err := os.Stat(*file)
	if err != nil {
		return err
	}
	progress, err := client.PushImage(ctx, *image, *tag, *file, false)
	if err != nil {
		return err
	}
	for update := range progress {
		if update.Error != nil {
			return update.Error
		}
		if update.Finished {
			fmt.Printf("deployed %s:%s\n", update.Image, update.Tag)
			return nil
		}
		percent := float64(update.BytesReceived) / float64(info.Size()) * 100
		fmt.Printf("\ruploaded %d/%d bytes (%.1f%%)", update.BytesReceived, info.Size(), percent)
	}
	return errors.New("deploy stream ended without success")
}

func listImages(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("list-images", flag.ContinueOnError)
	limit := fs.Int64("limit", -1, "maximum images to return; -1 uses the target default")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < -1 || *limit > 1<<31-1 {
		return errors.New("--limit must be between -1 and 2147483647")
	}

	images, err := client.ListImage(ctx, int32(*limit), nil)
	if err != nil {
		return err
	}
	return writeImages(os.Stdout, images)
}

func writeImages(w io.Writer, images <-chan *containerzclient.ImageInfo) error {
	table := tabwriter.NewWriter(w, 0, 8, 1, ' ', 0)
	if _, err := fmt.Fprintln(table, "ID\tNAME\tTAG"); err != nil {
		return err
	}
	for image := range images {
		if image.Error != nil {
			return image.Error
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\n", image.ID, image.ImageName, image.ImageTag); err != nil {
			return err
		}
	}
	return table.Flush()
}

func start(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	image := fs.String("image", "", "image name (required)")
	tag := fs.String("tag", "latest", "image tag")
	instance := fs.String("instance", "", "container instance name (required)")
	command := fs.String("command", "", "container command and arguments")
	network := fs.String("network", "host", "container network")
	restart := fs.String("restart", "none", "restart policy")
	cpus := fs.Float64("cpus", 0, "maximum CPUs; zero leaves it unset")
	softMemory := fs.Int64("soft-memory", 0, "soft memory limit in bytes")
	hardMemory := fs.Int64("hard-memory", 0, "hard memory limit in bytes")
	runAs := fs.String("run-as", "", "container user or user:group")
	var ports stringList
	var environment stringList
	var volumes stringList
	var devices stringList
	var capAdd stringList
	var capRemove stringList
	var labels stringList
	fs.Var(&ports, "port", "internal:external port mapping (repeatable)")
	fs.Var(&environment, "env", "KEY=VALUE environment variable (repeatable)")
	fs.Var(&volumes, "volume", "volume:mountpoint[:ro] (repeatable)")
	fs.Var(&devices, "device", "host-path:container-path[:rwm] (repeatable)")
	fs.Var(&capAdd, "cap-add", "Linux capability to add (repeatable)")
	fs.Var(&capRemove, "cap-remove", "Linux capability to remove (repeatable)")
	fs.Var(&labels, "label", "KEY=VALUE container label (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *image == "" {
		return errors.New("--image is required")
	}
	if *instance == "" {
		return errors.New("--instance is required")
	}
	labelMap, err := keyValueMap(labels)
	if err != nil {
		return fmt.Errorf("labels: %w", err)
	}

	options := []containerzclient.StartOption{
		containerzclient.WithNetwork(*network),
		containerzclient.WithRestartPolicy(*restart),
		containerzclient.WithCPUs(*cpus),
		containerzclient.WithSoftLimit(*softMemory),
		containerzclient.WithHardLimit(*hardMemory),
	}
	if len(ports) > 0 {
		options = append(options, containerzclient.WithPorts(ports))
	}
	if len(environment) > 0 {
		options = append(options, containerzclient.WithEnv(environment))
	}
	if len(volumes) > 0 {
		options = append(options, containerzclient.WithVolumes(volumes))
	}
	if len(devices) > 0 {
		options = append(options, containerzclient.WithDevices(devices))
	}
	if len(capAdd) > 0 || len(capRemove) > 0 {
		options = append(options, containerzclient.WithCapabilities(capAdd, capRemove))
	}
	if len(labelMap) > 0 {
		options = append(options, containerzclient.WithLabels(labelMap))
	}
	if *runAs != "" {
		options = append(options, containerzclient.WithRunAs(*runAs))
	}

	name, err := client.StartContainer(ctx, *image, *tag, *command, *instance, options...)
	if err != nil {
		return err
	}
	fmt.Printf("started %s\n", name)
	return nil
}

func createVolume(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("create-volume", flag.ContinueOnError)
	name := fs.String("name", "", "volume name")
	driver := fs.String("driver", "local", "volume driver")
	mountpoint := fs.String("mountpoint", "", "host path to bind (local driver shorthand)")
	var labels stringList
	var driverOptions stringList
	fs.Var(&labels, "label", "KEY=VALUE volume label (repeatable)")
	fs.Var(&driverOptions, "option", "KEY=VALUE driver option (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	labelMap, err := keyValueMap(labels)
	if err != nil {
		return fmt.Errorf("labels: %w", err)
	}
	optionMap, err := keyValueMap(driverOptions)
	if err != nil {
		return fmt.Errorf("driver options: %w", err)
	}
	if *mountpoint != "" {
		optionMap["type"] = "none"
		optionMap["options"] = "bind"
		optionMap["mountpoint"] = *mountpoint
	}

	created, err := client.CreateVolume(ctx, *name, *driver, labelMap, optionMap)
	if err != nil {
		return err
	}
	fmt.Printf("created volume %s\n", created)
	return nil
}

func keyValueMap(values []string) (map[string]string, error) {
	result := make(map[string]string, len(values))
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, fmt.Errorf("invalid KEY=VALUE %q", value)
		}
		result[parts[0]] = parts[1]
	}
	return result, nil
}

func removeVolume(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("remove-volume", flag.ContinueOnError)
	name := fs.String("name", "", "volume name")
	force := fs.Bool("force", false, "force volume removal")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return errors.New("--name is required")
	}
	if err := client.RemoveVolume(ctx, *name, *force); err != nil {
		return err
	}
	fmt.Printf("removed volume %s\n", *name)
	return nil
}

func list(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	all := fs.Bool("all", true, "include stopped containers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	containers, err := client.ListContainer(ctx, *all, 0, nil)
	if err != nil {
		return err
	}
	fmt.Printf("%-36s %-24s %-30s %s\n", "ID", "NAME", "IMAGE", "STATE")
	for container := range containers {
		if container.Error != nil {
			return container.Error
		}
		fmt.Printf("%-36s %-24s %-30s %s\n", container.ID, container.Name, container.ImageName, container.State)
	}
	return nil
}

func logs(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	instance := fs.String("instance", "", "container instance name (required)")
	follow := fs.Bool("follow", false, "follow logs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *instance == "" {
		return errors.New("--instance is required")
	}
	messages, err := client.Logs(ctx, *instance, *follow)
	if err != nil {
		return err
	}
	for message := range messages {
		if message.Error != nil {
			return message.Error
		}
		fmt.Print(message.Msg)
	}
	return nil
}

func stop(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	instance := fs.String("instance", "", "container instance name (required)")
	force := fs.Bool("force", false, "force termination")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *instance == "" {
		return errors.New("--instance is required")
	}
	if err := client.StopContainer(ctx, *instance, *force); err != nil {
		return err
	}
	fmt.Printf("stopped %s\n", *instance)
	return nil
}

func remove(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	instance := fs.String("instance", "", "container instance name (required)")
	force := fs.Bool("force", false, "remove a running instance")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *instance == "" {
		return errors.New("--instance is required")
	}
	if err := client.RemoveContainer(ctx, *instance, *force); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", *instance)
	return nil
}

func removeImage(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("remove-image", flag.ContinueOnError)
	image := fs.String("image", "", "image name (required)")
	tag := fs.String("tag", "latest", "image tag")
	force := fs.Bool("force", false, "force image removal")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *image == "" {
		return errors.New("--image is required")
	}
	if err := client.RemoveImage(ctx, *image, *tag, *force); err != nil {
		return err
	}
	fmt.Printf("removed %s:%s\n", *image, *tag)
	return nil
}

func cleanup(ctx context.Context, client *containerzclient.Client, args []string) error {
	fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	instance := fs.String("instance", "", "container instance name (required)")
	image := fs.String("image", "", "image name (required)")
	tag := fs.String("tag", "latest", "image tag")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *instance == "" {
		return errors.New("--instance is required")
	}
	if *image == "" {
		return errors.New("--image is required")
	}

	if err := client.StopContainer(ctx, *instance, true); err != nil {
		fmt.Fprintf(os.Stderr, "warning: stop %s: %v\n", *instance, err)
	}
	if err := client.RemoveContainer(ctx, *instance, true); err != nil {
		fmt.Fprintf(os.Stderr, "warning: remove %s: %v\n", *instance, err)
	}
	if err := client.RemoveImage(ctx, *image, *tag, true); err != nil {
		return err
	}
	fmt.Printf("cleaned up %s and %s:%s\n", *instance, *image, *tag)
	return nil
}

func dial(config connectionConfig) (*grpc.ClientConn, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         config.serverName,
		InsecureSkipVerify: config.insecureSkipVerify, //nolint:gosec -- explicit lab-only flag
	}
	if config.ca != "" {
		caPEM, err := os.ReadFile(config.ca)
		if err != nil {
			return nil, fmt.Errorf("read CA: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	if (config.cert == "") != (config.key == "") {
		return nil, errors.New("--cert and --key must be supplied together")
	}
	if config.cert != "" {
		certificate, err := tls.LoadX509KeyPair(config.cert, config.key)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	return grpc.NewClient(config.address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
}

func withAuthentication(ctx context.Context, config connectionConfig) context.Context {
	if config.username == "" {
		return ctx
	}
	password := os.Getenv(config.passwordEnv)
	return metadata.AppendToOutgoingContext(ctx, "username", config.username, "password", password)
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

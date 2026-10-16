<?php

// Run through the existing local Panel so credentials stay in its environment.
require 'vendor/autoload.php';
$app = require 'bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();

if (!$app->environment('local') || getenv('AQUADACTYL_LOCAL_SETUP') !== 'true') {
    throw new RuntimeException('Manta auto-setup only supports the local Aquadactyl test Panel.');
}

$dataRoot = getenv('MANTA_DATA_ROOT');
$subnet = getenv('MANTA_NETWORK_SUBNET');
$gateway = getenv('MANTA_NETWORK_GATEWAY');
if (!is_string($dataRoot) || !preg_match('#^/[\w./-]+$#', $dataRoot)
    || str_contains($dataRoot, '..') || !filter_var($gateway, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4)
    || !is_string($subnet) || !preg_match('#^\d+\.\d+\.\d+\.\d+/\d+$#', $subnet)) {
    throw new RuntimeException('Invalid Docker volume path or server network.');
}

$node = Illuminate\Support\Facades\DB::transaction(function () use ($app, $dataRoot) {
    $location = Pterodactyl\Models\Location::query()->firstOrCreate(['short' => 'local'], [
        'long' => 'Local Docker testing',
    ]);
    $node = Pterodactyl\Models\Node::query()->where('name', 'Local Manta')->first();
    if ($node && ($node->fqdn !== 'manta.localhost' || $node->daemonBase !== $dataRoot . '/volumes')) {
        throw new RuntimeException('An unrelated Local Manta node already exists; refusing to change it.');
    }
    if (!$node) {
        $node = $app->make(Pterodactyl\Services\Nodes\NodeCreationService::class)->handle([
            'name' => 'Local Manta',
            'description' => 'Manta development daemon for the local Aquadactyl Docker Panel.',
            'location_id' => $location->id,
            'fqdn' => 'manta.localhost',
            'scheme' => 'http',
            'behind_proxy' => false,
            'public' => false,
            'memory' => 4096,
            'memory_overallocate' => 0,
            'disk' => 16384,
            'disk_overallocate' => 0,
            'upload_size' => 100,
            'daemonListen' => 8083,
            'daemonSFTP' => 2023,
            'daemonBase' => $dataRoot . '/volumes',
        ]);
    }
    foreach (range(27015, 27019) as $port) {
        Pterodactyl\Models\Allocation::query()->firstOrCreate([
            'node_id' => $node->id,
            'ip' => '127.0.0.1',
            'port' => $port,
        ], ['ip_alias' => 'localhost']);
    }
    return $node;
});

$config = $node->getConfiguration();
$config['app_name'] = 'Manta';
$config['remote'] = 'http://panel';
$panelUrl = rtrim(config('app.url'), '/');
$config['allowed_origins'] = array_values(array_unique([
    $panelUrl,
    preg_replace('#://localhost(?=[:/]|$)#', '://127.0.0.1', $panelUrl),
]));
$config['ignore_panel_config_updates'] = true;
$config['system']['root_directory'] = $dataRoot;
$config['system']['log_directory'] = $dataRoot . '/logs';
$config['system']['archive_directory'] = $dataRoot . '/archives';
$config['system']['backup_directory'] = $dataRoot . '/backups';
$config['system']['tmp_directory'] = $dataRoot . '/tmp';
$config['system']['timezone'] = 'Europe/London';
$config['system']['machine_id']['directory'] = $dataRoot . '/machine-id';
$config['system']['passwd']['directory'] = $dataRoot . '/etc';
$config['docker']['network'] = [
    'name' => 'manta-test_servers',
    'network_mode' => 'manta-test_servers',
    'interface' => '127.0.0.1',
    'interfaces' => ['v4' => ['subnet' => $subnet, 'gateway' => $gateway]],
];

// The caller captures this response and pipes the YAML straight into the config
// volume. Never print this response in a terminal or commit it to the repo.
echo json_encode([
    'node_id' => $node->id,
    'name' => $node->name,
    'panel_url' => $panelUrl,
    'configuration' => Symfony\Component\Yaml\Yaml::dump($config, 8, 2, Symfony\Component\Yaml\Yaml::DUMP_EMPTY_ARRAY_AS_SEQUENCE),
], JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);

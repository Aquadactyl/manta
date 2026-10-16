<?php

require '/app/vendor/autoload.php';
$app = require '/app/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
if (!$app->environment('local') || getenv('AQUADACTYL_LOCAL_SETUP') !== 'true') {
    throw new RuntimeException('Telemetry checks are restricted to the local test Panel.');
}

$servers = Pterodactyl\Models\Server::query()->with('node')->whereIn('external_id', [
    'manta-benchmark-manta-paper', 'manta-benchmark-manta-bedrock',
    'manta-benchmark-wings-paper', 'manta-benchmark-wings-bedrock',
])->orderBy('id')->get();
if ($servers->count() !== 4) {
    throw new RuntimeException('Expected exactly four existing benchmark servers.');
}

$samples = [];
$first = [];
$last = [];
for ($round = 0; $round < 3; ++$round) {
    if ($round > 0) {
        sleep(2);
    }
    foreach ($servers as $server) {
        if (!in_array($server->node->name, ['Local Manta', 'Benchmark Wings'], true)
            || !str_starts_with($server->name, 'Benchmark ')) {
            throw new RuntimeException('Refusing to query an unrelated server.');
        }
        $response = $app->make(Pterodactyl\Repositories\Wings\DaemonServerRepository::class)
            ->setServer($server)->getDetails();
        $usage = $response['utilization'];
        if ($response['state'] !== 'running' || $usage['memory_bytes'] <= 0
            || $usage['memory_limit_bytes'] < $usage['memory_bytes'] || $usage['uptime'] <= 0
            || !is_finite((float) $usage['cpu_absolute']) || $usage['cpu_absolute'] < 0
            || $usage['network']['rx_bytes'] < 0 || $usage['network']['tx_bytes'] < 0) {
            throw new RuntimeException('Invalid live resource values for benchmark server '.$server->id);
        }
        if (isset($last[$server->uuid])) {
            foreach (['rx_bytes', 'tx_bytes'] as $counter) {
                if ($usage['network'][$counter] < $last[$server->uuid]['network'][$counter]) {
                    throw new RuntimeException('Network counter regressed during telemetry check.');
                }
            }
        }
        $first[$server->uuid] ??= $usage;
        $last[$server->uuid] = $usage;
        $samples[] = ['round' => $round, 'server_id' => $server->id, 'name' => $server->name,
            'node' => $server->node->name, 'utilization' => $usage];
    }
}
foreach ($servers as $server) {
    if ($last[$server->uuid]['uptime'] <= $first[$server->uuid]['uptime']) {
        throw new RuntimeException('Uptime did not advance for benchmark server '.$server->id);
    }
}
echo json_encode(['passed' => true, 'servers_checked' => 4, 'samples' => $samples], JSON_THROW_ON_ERROR | JSON_PRETTY_PRINT)."\n";

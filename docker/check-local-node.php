<?php

require 'vendor/autoload.php';
$app = require 'bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
if (!$app->environment('local') || getenv('AQUADACTYL_LOCAL_SETUP') !== 'true') {
    throw new RuntimeException('This check only supports the local Aquadactyl test Panel.');
}
$node = Pterodactyl\Models\Node::query()->where('name', 'Local Manta')->firstOrFail();
$repo = $app->make(Pterodactyl\Repositories\Wings\DaemonConfigurationRepository::class)->setNode($node);
$info = $repo->getSystemInformation();
if (!str_contains($info['version'] ?? '', 'manta')) {
    throw new RuntimeException('The node did not report a Manta build.');
}
echo json_encode([
    'node_id' => $node->id,
    'name' => $node->name,
    'address' => $node->getConnectionAddress(),
    'version' => $info['version'],
    'allocation_ports' => $node->allocations()->orderBy('port')->pluck('port'),
], JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);

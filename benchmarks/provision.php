<?php

use Pterodactyl\Models\{Allocation,Egg,Location,Node,Server,User};
use Pterodactyl\Services\{Nodes\NodeCreationService,Eggs\EggCreationService,Servers\ServerCreationService};

require '/app/vendor/autoload.php';
$app = require '/app/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
if (!$app->environment('local') || getenv('AQUADACTYL_LOCAL_SETUP') !== 'true') {
    throw new RuntimeException('Benchmark provisioning is restricted to the local test Panel.');
}
$action = $argv[1] ?? 'manifest';
$root = '/var/lib/docker/volumes/manta-benchmark-wings_data/_data';
if ($action === 'node') {
    $node = Node::query()->where('name','Benchmark Wings')->first();
    if (!$node) {
        $node = $app->make(NodeCreationService::class)->handle([
            'name'=>'Benchmark Wings','description'=>'Stock Pterodactyl Wings v1.13.3 comparison node.',
            'location_id'=>Location::query()->where('short','local')->firstOrFail()->id,
            'fqdn'=>'wings-benchmark.localhost','scheme'=>'http','behind_proxy'=>false,'public'=>false,
            'memory'=>4096,'disk'=>16384,'memory_overallocate'=>0,'disk_overallocate'=>0,
            'daemonListen'=>8084,'daemonSFTP'=>2024,'daemonBase'=>$root.'/volumes','upload_size'=>100,
        ]);
    }
    if ($node->fqdn !== 'wings-benchmark.localhost' || $node->daemonBase !== $root.'/volumes') {
        throw new RuntimeException('Benchmark Wings name is already used by an unrelated node.');
    }
    foreach(range(27115,27119) as $port) Allocation::query()->firstOrCreate([
        'node_id'=>$node->id,'ip'=>'127.0.0.1','port'=>$port,
    ],['ip_alias'=>'localhost']);
    $config=$node->getConfiguration();
    $config['remote']='http://panel';
    $config['allowed_origins']=['http://localhost:8081','http://127.0.0.1:8081'];
    $config['ignore_panel_config_updates']=true;
    $config['system']=array_replace_recursive($config['system'],[
        'root_directory'=>$root,'log_directory'=>$root.'/logs','archive_directory'=>$root.'/archives',
        'backup_directory'=>$root.'/backups','tmp_directory'=>$root.'/tmp','timezone'=>'Europe/London',
        'machine_id'=>['directory'=>$root.'/machine-id'],'passwd'=>['directory'=>$root.'/etc'],
    ]);
    $config['docker']['network']=['name'=>'manta-benchmark-wings_servers','network_mode'=>'manta-benchmark-wings_servers',
        'interface'=>'127.0.0.1','interfaces'=>['v4'=>['subnet'=>getenv('BENCH_SUBNET'),'gateway'=>getenv('BENCH_GATEWAY')]]];
    echo Symfony\Component\Yaml\Yaml::dump($config,8,2,Symfony\Component\Yaml\Yaml::DUMP_EMPTY_ARRAY_AS_SEQUENCE);
    exit;
}
if ($action === 'servers') {
    $owner=User::query()->where('username','repgraphics')->first() ?? User::query()->where('root_admin',true)->firstOrFail();
    $eggs=[];
    foreach(['paper'=>2,'bedrock'=>17] as $kind=>$sourceId) {
        $source=Egg::query()->findOrFail($sourceId);
        $name='Manta Benchmark '.ucfirst($kind);
        $egg=Egg::query()->where('name',$name)->first();
        if (!$egg) {
            $script=file_get_contents('/tmp/manta-benchmark-install-'.$kind.'.sh');
            $egg=$app->make(EggCreationService::class)->handle([
                'nest_id'=>$source->nest_id,'name'=>$name,'description'=>'Local reproducible benchmark using cached game binaries.',
                'docker_images'=>$source->docker_images,'startup'=>$source->startup,'config_from'=>$sourceId,
                'script_install'=>$script,'script_container'=>'ghcr.io/pterodactyl/installers:alpine','script_entry'=>'ash',
                'script_is_privileged'=>false,'features'=>$source->features,
            ]);
            foreach($source->variables as $variable) {
                $copy=$variable->replicate(); $copy->egg_id=$egg->id; $copy->save();
            }
        }
        $eggs[$kind]=$egg;
    }
    foreach(['manta'=>'Local Manta','wings'=>'Benchmark Wings'] as $variant=>$nodeName) {
        $node=Node::query()->where('name',$nodeName)->firstOrFail();
        foreach($eggs as $kind=>$egg) {
            $external='manta-benchmark-'.$variant.'-'.$kind;
            $server=Server::query()->where('external_id',$external)->first();
            if ($server) {
                if ($server->node_id!==$node->id || $server->egg_id!==$egg->id) throw new RuntimeException('Conflicting benchmark server.');
                continue;
            }
            $port=($variant==='manta'?27015:27115)+($kind==='bedrock'?1:0);
            $allocation=Allocation::query()->where('node_id',$node->id)->where('port',$port)->whereNull('server_id')->firstOrFail();
            $env=$egg->variables->pluck('default_value','env_variable')->all();
            if ($kind==='paper') { $env['MINECRAFT_VERSION']='26.3'; $env['SERVER_JARFILE']='server.jar'; }
            $data=[
                'external_id'=>$external,'name'=>'Benchmark '.ucfirst($variant).' '.ucfirst($kind),
                'description'=>'Dedicated matching Paper/Bedrock test server. No production data.',
                'owner_id'=>$owner->id,'node_id'=>$node->id,'allocation_id'=>$allocation->id,'egg_id'=>$egg->id,
                'memory'=>$kind==='paper'?1536:512,'swap'=>0,'disk'=>2048,'io'=>500,'cpu'=>200,
                'oom_disabled'=>false,'startup'=>$egg->startup,'image'=>$kind==='paper'?'ghcr.io/pterodactyl/yolks:java_25':'ghcr.io/ptero-eggs/yolks:debian',
                'environment'=>$env,'database_limit'=>0,'allocation_limit'=>2,'backup_limit'=>2,'start_on_completion'=>false,
            ];
            if ($kind==='bedrock') $data['allocation_additional']=[Allocation::query()->where('node_id',$node->id)->where('port',$port+1)->whereNull('server_id')->firstOrFail()->id];
            $server=$app->make(ServerCreationService::class)->handle($data);
            fwrite(STDERR,"Created {$server->name} ({$server->uuid}).\n");
        }
    }
}
$manifest=['nodes'=>[],'panel_origin'=>'http://localhost:8081'];
$admin=User::query()->where('username',getenv('AQUADACTYL_ADMIN_USERNAME')?:'admin')->firstOrFail();
$password=getenv('AQUADACTYL_ADMIN_PASSWORD');
if (!$password || !Illuminate\Support\Facades\Hash::check($password,$admin->password)) throw new RuntimeException('Local SFTP test credentials do not match the configured administrator.');
$manifest['sftp']=['username'=>$admin->username,'password'=>$password,'user_uuid'=>$admin->uuid];
foreach(['manta'=>'Local Manta','wings'=>'Benchmark Wings'] as $variant=>$name) {
    $node=Node::query()->where('name',$name)->firstOrFail();
    $servers=Server::query()->where('node_id',$node->id)->where('external_id','like','manta-benchmark-%')->get();
    $manifest['nodes'][$variant]=[
        'id'=>$node->id,'name'=>$name,'container'=>$variant==='manta'?'manta-test':'wings-benchmark',
        'base'=>$variant==='manta'?'http://manta-test:8083':'http://wings-benchmark:8084',
        'sftp_port'=>$node->daemonSFTP,'token'=>$node->getDecryptedKey(),'data_root'=>dirname($node->daemonBase),
        'servers'=>$servers->map(fn($s)=>['id'=>$s->id,'uuid'=>$s->uuid,'name'=>$s->name,
            'kind'=>str_ends_with($s->external_id,'paper')?'paper':'bedrock','identifier'=>$s->identifier,'port'=>$s->allocation->port,'status'=>$s->status])->all(),
    ];
}
// Credentials are captured privately by the host script and mounted read-only
// into the isolated runner. Never log the manifest or retain it in reports.
echo json_encode($manifest,JSON_THROW_ON_ERROR|JSON_UNESCAPED_SLASHES);

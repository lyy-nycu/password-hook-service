<?php

$payload = json_encode([
    'cn' => '311551001',
    'password' => 'cleartext_password',
    'displayName' => 'Test User',
    'mail' => 'test@nycu.edu.tw',
]);

$timestamp = time();
$nonce = bin2hex(random_bytes(16));
$secret = getenv('HOOK_HMAC_SECRET');
$signature = hash_hmac('sha256', $timestamp . '.' . $nonce . '.' . $payload, $secret);

echo "X-Hook-Timestamp: {$timestamp}\n";
echo "X-Hook-Nonce: {$nonce}\n";
echo "X-Hook-Signature: sha256={$signature}\n";
echo "{$payload}\n";

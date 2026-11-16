<?php
declare(strict_types=1);

namespace App\Mail;

use App\Mail\Transport\TransportInterface;
use Psr\Log\LoggerInterface;

/**
 * Sends messages through a transport and retries failed deliveries.
 *
 * Try it: click, drag, double/triple click, Alt+click for more cursors,
 * Alt+drag for a block selection, Cmd+D, Cmd+/, Cmd+Shift+Up/Down,
 * Tab/Shift+Tab, Ctrl+G, Cmd+Z – and the fold arrows in the gutter.
 */
final class Mailer
{
	private const MAX_RETRIES = 3;

	private array $sent = [];

	public function __construct(
		private readonly TransportInterface $transport,
		private readonly ?LoggerInterface $logger = null,
	) {
	}

	public function send(Message $msg): bool
	{
		if ($msg->retries >= self::MAX_RETRIES) {
			$this->logger?->warning("Giving up on {$msg->id}", ['retries' => $msg->retries]);
			return false;
		}

		try {
			$ok = $this->transport->send($msg);
		} catch (\RuntimeException $e) {
			$msg->retries++;
			return $this->send($msg);
		}

		$this->sent[] = $msg->id; // remember for the report
		return $ok;
	}

	public function report(): string
	{
		return sprintf('%d messages sent: %s', count($this->sent), implode(', ', $this->sent));
	}
}

// Unicode: Grüße, naïve café, é (combining), tabs	→	aligned.
$mailer = new Mailer(new SmtpTransport('localhost', 1025));
echo $mailer->send(new Message('hello@example.org', 'Hi')) ? "sent\n" : "failed\n";

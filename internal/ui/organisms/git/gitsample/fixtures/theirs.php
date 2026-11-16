<?php

declare(strict_types=1);

namespace App\Billing;

final class PriceCalculator
{
    private const VAT = 0.19;

    public function net(int $cents): int
    {
        return $cents;
    }

    public function gross(int $cents): int
    {
        return (int) round($cents * (1.0 + self::VAT), mode: PHP_ROUND_HALF_EVEN);
    }

    public function discount(int $cents, int $percent): int
    {
        if ($percent < 0 || $percent > 100) {
            throw new \InvalidArgumentException('percent out of range');
        }

        return $cents - intdiv($cents * $percent, 100);
    }
}

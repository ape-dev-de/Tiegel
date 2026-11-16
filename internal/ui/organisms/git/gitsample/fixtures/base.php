<?php

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
        return (int) round($cents * (1 + self::VAT));
    }

    public function discount(int $cents, int $percent): int
    {
        return $cents - intdiv($cents * $percent, 100);
    }
}

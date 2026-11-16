<?php

namespace App\Billing;

final class PriceCalculator
{
    private const VAT = 0.19;
    private const REDUCED_VAT = 0.07;

    public function net(int $cents): int
    {
        return $cents;
    }

    public function gross(int $cents, bool $reduced = false): int
    {
        $rate = $reduced ? self::REDUCED_VAT : self::VAT;

        return (int) round($cents * (1 + $rate));
    }

    public function discount(int $cents, int $percent): int
    {
        return $cents - intdiv($cents * $percent, 100);
    }
}

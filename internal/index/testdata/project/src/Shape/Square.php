<?php
namespace App\Shape;

use App\Contracts;

class Square extends AbstractShape
{
    public function __construct(protected float $side) {}

    public function area(): float
    {
        return $this->side * $this->side;
    }

    public function name(): string
    {
        return 'square';
    }

    public static function unit(): static
    {
        return new static(1.0);
    }

    public function check(Contracts\Shape $s): bool
    {
        return $s instanceof Contracts\HasName;
    }
}

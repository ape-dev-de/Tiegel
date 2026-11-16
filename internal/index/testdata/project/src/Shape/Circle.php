<?php
namespace App\Shape;

final class Circle extends AbstractShape
{
    public function __construct(private float $r) {}

    public function area(): float
    {
        return M_PI * $this->r ** 2 + parent::count();
    }
}

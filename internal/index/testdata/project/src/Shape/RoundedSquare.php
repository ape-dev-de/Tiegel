<?php
namespace App\Shape;

class RoundedSquare extends Square
{
    public function area(): float
    {
        return parent::area() - 0.1;
    }
}

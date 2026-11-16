<?php
namespace App\Contracts;

interface Shape extends HasName, \Countable
{
    const SIDES = 0;

    public function area(): float;
}

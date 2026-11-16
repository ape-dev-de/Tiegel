<?php
namespace App\Util;

trait Describes
{
    public function describe(): string
    {
        return 'I am ' . $this->name();
    }
}

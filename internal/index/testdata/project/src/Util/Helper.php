<?php
namespace App\Util;

const VERSION = '1.0';

function helper(string $s): string
{
    return $s;
}

class Helper
{
    public static function write(string $s): void
    {
        echo helper($s), VERSION, \strlen($s);
    }
}

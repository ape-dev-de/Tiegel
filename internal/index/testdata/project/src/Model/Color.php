<?php
namespace App\Model;

use App\Contracts\HasName;

enum Color: string implements HasName
{
    case Red = 'red';
    case Blue = 'blue';

    public function name(): string
    {
        return $this->value;
    }

    public static function default(): self
    {
        return self::Red;
    }
}

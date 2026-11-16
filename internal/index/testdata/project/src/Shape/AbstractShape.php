<?php
namespace App\Shape;

use App\Contracts\Shape as ShapeContract;
use App\Util\{Describes, Helper as H};
use Acme\Lib\Loggable;

abstract class AbstractShape implements ShapeContract, Loggable
{
    use Describes;

    protected string $label = '';

    abstract public function area(): float;

    public function name(): string
    {
        return static::class;
    }

    public function count(): int
    {
        return 1;
    }

    public function log(string $msg): void
    {
        H::write($msg);
    }

    public function show(): string
    {
        return $this->describe() . $this->area() . self::KIND . $this->label;
    }

    const KIND = 'shape';
}

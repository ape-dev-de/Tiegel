<?php
namespace App\Other;

use App\Shape\Circle;
use App\Shape\Square as Sq;
use App\Model\Shape;
use App\Contracts\Shape as ShapeInterface;
use App\Model\Color;
use function App\Util\helper;
use const App\Util\VERSION;

class Usage
{
    public function run(ShapeInterface $s): Shape
    {
        $c = new Circle(1.0);
        $q = Sq::unit();
        try {
            helper(VERSION);
        } catch (\RuntimeException | NotFound $e) {
        }
        $x = Color::Red;
        $y = ShapeInterface::SIDES;
        return new Shape();
    }
}
